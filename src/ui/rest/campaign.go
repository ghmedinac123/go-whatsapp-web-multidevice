package rest

import (
	"errors"
	"io"
	"strconv"
	"strings"
	"time"

	domainCampaign "github.com/aldinokemal/go-whatsapp-web-multidevice/domains/campaign"
	campaigninfra "github.com/aldinokemal/go-whatsapp-web-multidevice/infrastructure/campaign"
	"github.com/aldinokemal/go-whatsapp-web-multidevice/pkg/utils"
	"github.com/gofiber/fiber/v2"
	"github.com/sirupsen/logrus"
)

// CampaignHandler exposes CRUD + lifecycle + recipient/template management for the
// mass-messaging module. Lifecycle actions delegate to the campaign Manager, which
// owns the running goroutines.
type CampaignHandler struct {
	Repo     domainCampaign.ICampaignRepository
	Template domainCampaign.ITemplateRepository
	Manager  *campaigninfra.Manager
}

// NewCampaignHandler wires the handler.
func NewCampaignHandler(
	repo domainCampaign.ICampaignRepository,
	tpl domainCampaign.ITemplateRepository,
	manager *campaigninfra.Manager,
) *CampaignHandler {
	return &CampaignHandler{Repo: repo, Template: tpl, Manager: manager}
}

// RegisterCampaignRoutes mounts every campaign route on the given router. These
// are admin/config routes (not device-scoped), so they are registered before the
// device middleware.
func RegisterCampaignRoutes(router fiber.Router, h *CampaignHandler) {
	// Static segment route registered before "/campaigns/:id" so it is unambiguous.
	router.Post("/campaigns/import/analyze", h.AnalyzeImport)

	router.Get("/campaigns", h.List)
	router.Post("/campaigns", h.Create)
	router.Get("/campaigns/:id", h.Get)
	router.Put("/campaigns/:id", h.Update)
	router.Delete("/campaigns/:id", h.Delete)

	router.Post("/campaigns/:id/start", h.Start)
	router.Post("/campaigns/:id/pause", h.Pause)
	router.Post("/campaigns/:id/resume", h.Resume)
	router.Post("/campaigns/:id/cancel", h.Cancel)

	router.Get("/campaigns/:id/recipients", h.ListRecipients)
	router.Post("/campaigns/:id/recipients", h.ImportRecipients)
	router.Get("/campaigns/:id/variables", h.Variables)
	router.Get("/campaigns/:id/stats", h.Stats)

	router.Get("/campaigns/:id/senders", h.ListSenders)
	router.Post("/campaigns/:id/senders", h.AddSender)
	router.Delete("/campaigns/:id/senders/:senderId", h.DeleteSender)

	router.Get("/campaign-templates", h.ListTemplates)
	router.Post("/campaign-templates", h.CreateTemplate)
	router.Put("/campaign-templates/:id", h.UpdateTemplate)
	router.Delete("/campaign-templates/:id", h.DeleteTemplate)
}

// --- request shapes ---

type campaignRequest struct {
	Name          string  `json:"name"`
	TemplateBody  string  `json:"template_body"`
	TemplateMedia string  `json:"template_media"`
	ScheduleAt    *string `json:"schedule_at"` // RFC3339, optional
	Status        string  `json:"status"`
}

type senderRequest struct {
	DeviceID string `json:"device_id"`
	MaxDaily int    `json:"max_daily"`
}

type templateRequest struct {
	Name     string `json:"name"`
	Body     string `json:"body"`
	MediaURL string `json:"media_url"`
	Category string `json:"category"`
}

// --- campaigns ---

func (h *CampaignHandler) List(c *fiber.Ctx) error {
	campaigns, err := h.Repo.ListCampaigns()
	if err != nil {
		return h.internalError(c, err)
	}
	if campaigns == nil {
		campaigns = []*domainCampaign.Campaign{}
	}
	return ok(c, "Campaigns", campaigns)
}

func (h *CampaignHandler) Create(c *fiber.Ctx) error {
	var req campaignRequest
	if err := c.BodyParser(&req); err != nil {
		return utils.ResponseError(c, "Invalid payload")
	}
	schedule, err := parseSchedule(req.ScheduleAt)
	if err != nil {
		return utils.ResponseError(c, err.Error())
	}
	camp := &domainCampaign.Campaign{
		Name:          strings.TrimSpace(req.Name),
		TemplateBody:  req.TemplateBody,
		TemplateMedia: strings.TrimSpace(req.TemplateMedia),
		ScheduleAt:    schedule,
		Status:        req.Status,
	}
	if err := h.Repo.CreateCampaign(camp); err != nil {
		return utils.ResponseError(c, err.Error())
	}
	return c.Status(fiber.StatusCreated).JSON(utils.ResponseData{
		Code: "SUCCESS", Message: "Campaign created", Results: camp,
	})
}

func (h *CampaignHandler) Get(c *fiber.Ctx) error {
	id, err := idParam(c, "id")
	if err != nil {
		return utils.ResponseError(c, err.Error())
	}
	camp, err := h.Repo.GetCampaign(id)
	if err != nil {
		return h.internalError(c, err)
	}
	if camp == nil {
		return notFound(c, "Campaign not found")
	}
	senders, _ := h.Repo.ListSenders(id)
	stats, _ := h.Manager.Stats(id)
	return ok(c, "Campaign", fiber.Map{
		"campaign": camp,
		"senders":  nonNilSenders(senders),
		"stats":    stats,
		"running":  h.Manager.IsRunning(id),
	})
}

func (h *CampaignHandler) Update(c *fiber.Ctx) error {
	id, err := idParam(c, "id")
	if err != nil {
		return utils.ResponseError(c, err.Error())
	}
	existing, err := h.Repo.GetCampaign(id)
	if err != nil {
		return h.internalError(c, err)
	}
	if existing == nil {
		return notFound(c, "Campaign not found")
	}
	var req campaignRequest
	if err := c.BodyParser(&req); err != nil {
		return utils.ResponseError(c, "Invalid payload")
	}
	schedule, err := parseSchedule(req.ScheduleAt)
	if err != nil {
		return utils.ResponseError(c, err.Error())
	}
	existing.Name = strings.TrimSpace(req.Name)
	existing.TemplateBody = req.TemplateBody
	existing.TemplateMedia = strings.TrimSpace(req.TemplateMedia)
	existing.ScheduleAt = schedule
	if req.Status != "" {
		existing.Status = req.Status
	}
	if err := h.Repo.UpdateCampaign(existing); err != nil {
		return utils.ResponseError(c, err.Error())
	}
	return ok(c, "Campaign updated", existing)
}

func (h *CampaignHandler) Delete(c *fiber.Ctx) error {
	id, err := idParam(c, "id")
	if err != nil {
		return utils.ResponseError(c, err.Error())
	}
	if h.Manager.IsRunning(id) {
		return utils.ResponseError(c, "Stop the campaign before deleting it")
	}
	if err := h.Repo.DeleteCampaign(id); err != nil {
		return h.internalError(c, err)
	}
	return ok(c, "Campaign deleted", nil)
}

// --- lifecycle ---

func (h *CampaignHandler) Start(c *fiber.Ctx) error {
	return h.lifecycle(c, h.Manager.Start, "Campaign started")
}
func (h *CampaignHandler) Pause(c *fiber.Ctx) error {
	return h.lifecycle(c, h.Manager.Pause, "Campaign paused")
}
func (h *CampaignHandler) Resume(c *fiber.Ctx) error {
	return h.lifecycle(c, h.Manager.Resume, "Campaign resumed")
}
func (h *CampaignHandler) Cancel(c *fiber.Ctx) error {
	return h.lifecycle(c, h.Manager.Cancel, "Campaign cancelled")
}

func (h *CampaignHandler) lifecycle(c *fiber.Ctx, action func(int) error, message string) error {
	id, err := idParam(c, "id")
	if err != nil {
		return utils.ResponseError(c, err.Error())
	}
	if err := action(id); err != nil {
		if errors.Is(err, campaigninfra.ErrCampaignNotFound) {
			return notFound(c, "Campaign not found")
		}
		return utils.ResponseError(c, err.Error())
	}
	stats, _ := h.Manager.Stats(id)
	return ok(c, message, stats)
}

// --- recipients ---

func (h *CampaignHandler) ListRecipients(c *fiber.Ctx) error {
	id, err := idParam(c, "id")
	if err != nil {
		return utils.ResponseError(c, err.Error())
	}
	status := strings.TrimSpace(c.Query("status"))
	limit, _ := strconv.Atoi(c.Query("limit"))
	recipients, err := h.Repo.ListRecipients(id, status, limit)
	if err != nil {
		return h.internalError(c, err)
	}
	if recipients == nil {
		recipients = []*domainCampaign.Recipient{}
	}
	return ok(c, "Recipients", recipients)
}

// ImportRecipients accepts a JSON array body, a CSV body (?format=csv or text/csv),
// or a multipart file upload (field "file"). Duplicates and invalid phones are
// dropped during parsing; existing phones are ignored at insert time.
func (h *CampaignHandler) ImportRecipients(c *fiber.Ctx) error {
	id, err := idParam(c, "id")
	if err != nil {
		return utils.ResponseError(c, err.Error())
	}
	if camp, err := h.Repo.GetCampaign(id); err != nil {
		return h.internalError(c, err)
	} else if camp == nil {
		return notFound(c, "Campaign not found")
	}

	data, format, err := readImportPayload(c)
	if err != nil {
		return utils.ResponseError(c, err.Error())
	}
	opts := campaigninfra.ImportOptions{
		PhoneColumn: strings.TrimSpace(c.Query("phone_column")),
		NameColumn:  strings.TrimSpace(c.Query("name_column")),
	}
	recipients, err := campaigninfra.ParseRecipients(data, format, opts)
	if err != nil {
		return utils.ResponseError(c, err.Error())
	}
	if len(recipients) == 0 {
		return utils.ResponseError(c, "No valid recipients found in payload")
	}
	batchSize, _ := strconv.Atoi(c.Query("batch_size"))
	added, err := h.Repo.AddRecipients(id, recipients, batchSize)
	if err != nil {
		return h.internalError(c, err)
	}
	return ok(c, "Recipients imported", fiber.Map{
		"parsed":   len(recipients),
		"imported": added,
		"skipped":  len(recipients) - added,
	})
}

// AnalyzeImport inspects an uploaded payload (file or body) and returns its
// columns and usable {tags} without inserting anything.
func (h *CampaignHandler) AnalyzeImport(c *fiber.Ctx) error {
	data, format, err := readImportPayload(c)
	if err != nil {
		return utils.ResponseError(c, err.Error())
	}
	analysis, err := campaigninfra.AnalyzeImport(data, format)
	if err != nil {
		return utils.ResponseError(c, err.Error())
	}
	return ok(c, "Import analysis", analysis)
}

// Variables returns the {tags} usable in a campaign's template, derived from the
// variable keys of its imported recipients plus the always-available nombre/phone.
func (h *CampaignHandler) Variables(c *fiber.Ctx) error {
	id, err := idParam(c, "id")
	if err != nil {
		return utils.ResponseError(c, err.Error())
	}
	keys, err := h.Repo.VariableKeys(id, 1000)
	if err != nil {
		return h.internalError(c, err)
	}
	tags := make([]string, len(keys))
	for i, k := range keys {
		tags[i] = "{" + k + "}"
	}
	return ok(c, "Variables", fiber.Map{"keys": keys, "tags": tags})
}

func (h *CampaignHandler) Stats(c *fiber.Ctx) error {
	id, err := idParam(c, "id")
	if err != nil {
		return utils.ResponseError(c, err.Error())
	}
	stats, err := h.Manager.Stats(id)
	if err != nil {
		if errors.Is(err, campaigninfra.ErrCampaignNotFound) {
			return notFound(c, "Campaign not found")
		}
		return h.internalError(c, err)
	}
	return ok(c, "Stats", stats)
}

// --- senders ---

func (h *CampaignHandler) ListSenders(c *fiber.Ctx) error {
	id, err := idParam(c, "id")
	if err != nil {
		return utils.ResponseError(c, err.Error())
	}
	senders, err := h.Repo.ListSenders(id)
	if err != nil {
		return h.internalError(c, err)
	}
	return ok(c, "Senders", nonNilSenders(senders))
}

func (h *CampaignHandler) AddSender(c *fiber.Ctx) error {
	id, err := idParam(c, "id")
	if err != nil {
		return utils.ResponseError(c, err.Error())
	}
	var req senderRequest
	if err := c.BodyParser(&req); err != nil {
		return utils.ResponseError(c, "Invalid payload")
	}
	if strings.TrimSpace(req.DeviceID) == "" {
		return utils.ResponseError(c, "device_id is required")
	}
	sender := &domainCampaign.Sender{
		CampaignID: id,
		DeviceID:   strings.TrimSpace(req.DeviceID),
		MaxDaily:   req.MaxDaily,
		Enabled:    true,
	}
	if err := h.Repo.AddSender(sender); err != nil {
		return utils.ResponseError(c, err.Error())
	}
	return c.Status(fiber.StatusCreated).JSON(utils.ResponseData{
		Code: "SUCCESS", Message: "Sender added", Results: sender,
	})
}

func (h *CampaignHandler) DeleteSender(c *fiber.Ctx) error {
	senderID, err := idParam(c, "senderId")
	if err != nil {
		return utils.ResponseError(c, err.Error())
	}
	if err := h.Repo.DeleteSender(senderID); err != nil {
		return h.internalError(c, err)
	}
	return ok(c, "Sender removed", nil)
}

// --- templates ---

func (h *CampaignHandler) ListTemplates(c *fiber.Ctx) error {
	templates, err := h.Template.ListTemplates()
	if err != nil {
		return h.internalError(c, err)
	}
	if templates == nil {
		templates = []*domainCampaign.Template{}
	}
	return ok(c, "Templates", templates)
}

func (h *CampaignHandler) CreateTemplate(c *fiber.Ctx) error {
	var req templateRequest
	if err := c.BodyParser(&req); err != nil {
		return utils.ResponseError(c, "Invalid payload")
	}
	tpl := &domainCampaign.Template{
		Name:     strings.TrimSpace(req.Name),
		Body:     req.Body,
		MediaURL: strings.TrimSpace(req.MediaURL),
		Category: strings.TrimSpace(req.Category),
	}
	if err := h.Template.CreateTemplate(tpl); err != nil {
		return utils.ResponseError(c, err.Error())
	}
	return c.Status(fiber.StatusCreated).JSON(utils.ResponseData{
		Code: "SUCCESS", Message: "Template created", Results: tpl,
	})
}

func (h *CampaignHandler) UpdateTemplate(c *fiber.Ctx) error {
	id, err := idParam(c, "id")
	if err != nil {
		return utils.ResponseError(c, err.Error())
	}
	var req templateRequest
	if err := c.BodyParser(&req); err != nil {
		return utils.ResponseError(c, "Invalid payload")
	}
	tpl := &domainCampaign.Template{
		ID:       id,
		Name:     strings.TrimSpace(req.Name),
		Body:     req.Body,
		MediaURL: strings.TrimSpace(req.MediaURL),
		Category: strings.TrimSpace(req.Category),
	}
	if err := h.Template.UpdateTemplate(tpl); err != nil {
		return utils.ResponseError(c, err.Error())
	}
	return ok(c, "Template updated", tpl)
}

func (h *CampaignHandler) DeleteTemplate(c *fiber.Ctx) error {
	id, err := idParam(c, "id")
	if err != nil {
		return utils.ResponseError(c, err.Error())
	}
	if err := h.Template.DeleteTemplate(id); err != nil {
		return h.internalError(c, err)
	}
	return ok(c, "Template deleted", nil)
}

// --- helpers ---

func idParam(c *fiber.Ctx, name string) (int, error) {
	id, err := strconv.Atoi(c.Params(name))
	if err != nil || id <= 0 {
		return 0, errors.New(name + " must be a positive integer")
	}
	return id, nil
}

// parseSchedule turns an optional RFC3339 string into a *time.Time.
func parseSchedule(s *string) (*time.Time, error) {
	if s == nil || strings.TrimSpace(*s) == "" {
		return nil, nil
	}
	t, err := time.Parse(time.RFC3339, strings.TrimSpace(*s))
	if err != nil {
		return nil, errors.New("schedule_at must be RFC3339 (e.g. 2026-05-28T08:00:00Z)")
	}
	return &t, nil
}

// readImportPayload extracts the recipient payload bytes and its format
// ("csv"/"xlsx"/"json"), from either a multipart file upload or the raw body.
func readImportPayload(c *fiber.Ctx) ([]byte, string, error) {
	if fh, err := c.FormFile("file"); err == nil && fh != nil {
		f, err := fh.Open()
		if err != nil {
			return nil, "", errors.New("cannot open uploaded file")
		}
		defer f.Close()
		buf, err := io.ReadAll(f)
		if err != nil {
			return nil, "", errors.New("cannot read uploaded file")
		}
		return buf, formatFromFilename(fh.Filename), nil
	}

	body := c.Body()
	if len(body) == 0 {
		return nil, "", errors.New("empty request body")
	}
	return body, formatFromRequest(c), nil
}

// formatFromFilename maps a file extension to an import format, defaulting to CSV.
func formatFromFilename(name string) string {
	switch {
	case strings.HasSuffix(strings.ToLower(name), ".xlsx"):
		return campaigninfra.FormatXLSX
	case strings.HasSuffix(strings.ToLower(name), ".json"):
		return campaigninfra.FormatJSON
	default:
		return campaigninfra.FormatCSV
	}
}

// formatFromRequest reads the ?format query or Content-Type for a raw body,
// defaulting to JSON (the shape the UI posts).
func formatFromRequest(c *fiber.Ctx) string {
	switch strings.ToLower(strings.TrimSpace(c.Query("format"))) {
	case campaigninfra.FormatCSV:
		return campaigninfra.FormatCSV
	case campaigninfra.FormatXLSX:
		return campaigninfra.FormatXLSX
	case campaigninfra.FormatJSON:
		return campaigninfra.FormatJSON
	}
	ct := strings.ToLower(c.Get("Content-Type"))
	switch {
	case strings.Contains(ct, "csv"):
		return campaigninfra.FormatCSV
	case strings.Contains(ct, "spreadsheet") || strings.Contains(ct, "xlsx"):
		return campaigninfra.FormatXLSX
	default:
		return campaigninfra.FormatJSON
	}
}

func nonNilSenders(senders []*domainCampaign.Sender) []*domainCampaign.Sender {
	if senders == nil {
		return []*domainCampaign.Sender{}
	}
	return senders
}

func ok(c *fiber.Ctx, message string, results any) error {
	return c.JSON(utils.ResponseData{Code: "SUCCESS", Message: message, Results: results})
}

func notFound(c *fiber.Ctx, message string) error {
	return c.Status(fiber.StatusNotFound).JSON(utils.ResponseData{Code: "NOT_FOUND", Message: message})
}

func (h *CampaignHandler) internalError(c *fiber.Ctx, err error) error {
	logrus.Errorf("Campaign: %v", err)
	return c.Status(fiber.StatusInternalServerError).JSON(utils.ResponseData{
		Code: "INTERNAL_ERROR", Message: "Internal server error",
	})
}
