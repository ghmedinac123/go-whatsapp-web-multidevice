package campaign

import (
	"database/sql"
	"path/filepath"
	"testing"

	domainCampaign "github.com/aldinokemal/go-whatsapp-web-multidevice/domains/campaign"
	"github.com/aldinokemal/go-whatsapp-web-multidevice/pkg/sqlite"
)

func newTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open(sqlite.DriverName, filepath.Join(t.TempDir(), "chatstorage.db"))
	if err != nil {
		t.Fatalf("open sqlite database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func newTestRepo(t *testing.T) *SQLiteRepository {
	t.Helper()
	repo := &SQLiteRepository{db: newTestDB(t)}
	if err := repo.Migrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	// Migrate must be idempotent.
	if err := repo.Migrate(); err != nil {
		t.Fatalf("migrate (second call): %v", err)
	}
	return repo
}

func TestCampaignCRUD(t *testing.T) {
	repo := newTestRepo(t)

	c := &domainCampaign.Campaign{Name: "Promo", TemplateBody: "{Hola|Buenas} {nombre}"}
	if err := repo.CreateCampaign(c); err != nil {
		t.Fatalf("create: %v", err)
	}
	if c.ID == 0 {
		t.Fatal("expected campaign id to be filled")
	}
	if c.Status != domainCampaign.StatusDraft {
		t.Fatalf("expected default draft status, got %q", c.Status)
	}

	got, err := repo.GetCampaign(c.ID)
	if err != nil || got == nil {
		t.Fatalf("get: %v (got=%v)", err, got)
	}
	if got.Name != "Promo" || got.TemplateBody != c.TemplateBody {
		t.Fatalf("round-trip mismatch: %+v", got)
	}

	if err := repo.SetCampaignStatus(c.ID, domainCampaign.StatusRunning); err != nil {
		t.Fatalf("set status: %v", err)
	}
	got, _ = repo.GetCampaign(c.ID)
	if got.Status != domainCampaign.StatusRunning {
		t.Fatalf("expected running, got %q", got.Status)
	}

	list, err := repo.ListCampaigns()
	if err != nil || len(list) != 1 {
		t.Fatalf("list: %v len=%d", err, len(list))
	}

	if err := repo.DeleteCampaign(c.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	got, _ = repo.GetCampaign(c.ID)
	if got != nil {
		t.Fatalf("expected nil after delete, got %+v", got)
	}
}

func TestRecipientDedupAndStatus(t *testing.T) {
	repo := newTestRepo(t)
	c := &domainCampaign.Campaign{Name: "C", TemplateBody: "hi"}
	if err := repo.CreateCampaign(c); err != nil {
		t.Fatalf("create campaign: %v", err)
	}

	recipients := []*domainCampaign.Recipient{
		{Phone: "573166203787", Name: "Gerlen", Variables: map[string]string{"empresa": "Fututel"}},
		{Phone: "573001112233", Name: "Ana"},
		{Phone: "573166203787", Name: "dup"}, // duplicate phone -> ignored
	}
	added, err := repo.AddRecipients(c.ID, recipients)
	if err != nil {
		t.Fatalf("add recipients: %v", err)
	}
	if added != 2 {
		t.Fatalf("expected 2 inserted (dedup), got %d", added)
	}

	// Adding the same phones again inserts nothing.
	added, err = repo.AddRecipients(c.ID, recipients)
	if err != nil {
		t.Fatalf("add recipients (2nd): %v", err)
	}
	if added != 0 {
		t.Fatalf("expected 0 inserted on re-import, got %d", added)
	}

	next, err := repo.NextPendingRecipient(c.ID)
	if err != nil || next == nil {
		t.Fatalf("next pending: %v (rec=%v)", err, next)
	}
	if next.Variables["empresa"] != "Fututel" {
		t.Fatalf("variables not round-tripped: %+v", next.Variables)
	}

	if err := repo.MarkRecipientSent(next.ID, "device-a"); err != nil {
		t.Fatalf("mark sent: %v", err)
	}

	counts, err := repo.CountRecipientsByStatus(c.ID)
	if err != nil {
		t.Fatalf("counts: %v", err)
	}
	if counts[domainCampaign.RecipientSent] != 1 || counts[domainCampaign.RecipientPending] != 1 {
		t.Fatalf("unexpected counts: %+v", counts)
	}

	// The sent recipient must no longer be returned as pending.
	next2, _ := repo.NextPendingRecipient(c.ID)
	if next2 == nil || next2.ID == next.ID {
		t.Fatalf("expected a different pending recipient, got %+v", next2)
	}
}

func TestSenderPoolPersistence(t *testing.T) {
	repo := newTestRepo(t)
	c := &domainCampaign.Campaign{Name: "C", TemplateBody: "hi"}
	if err := repo.CreateCampaign(c); err != nil {
		t.Fatalf("create campaign: %v", err)
	}

	s := &domainCampaign.Sender{CampaignID: c.ID, DeviceID: "dev-1", Enabled: true}
	if err := repo.AddSender(s); err != nil {
		t.Fatalf("add sender: %v", err)
	}
	if s.MaxDaily != 200 || s.HealthScore != 1.0 {
		t.Fatalf("expected defaults max_daily=200 health=1.0, got %+v", s)
	}

	s.SentToday = 5
	s.HealthScore = 0.7
	if err := repo.UpdateSenderStats(s); err != nil {
		t.Fatalf("update sender: %v", err)
	}

	senders, err := repo.ListSenders(c.ID)
	if err != nil || len(senders) != 1 {
		t.Fatalf("list senders: %v len=%d", err, len(senders))
	}
	if senders[0].SentToday != 5 || senders[0].HealthScore != 0.7 {
		t.Fatalf("stats not persisted: %+v", senders[0])
	}

	if err := repo.ResetDailySent(c.ID); err != nil {
		t.Fatalf("reset daily: %v", err)
	}
	senders, _ = repo.ListSenders(c.ID)
	if senders[0].SentToday != 0 {
		t.Fatalf("expected sent_today reset to 0, got %d", senders[0].SentToday)
	}
}

func TestTemplateCRUD(t *testing.T) {
	repo := &TemplateRepository{db: newTestDB(t)}
	if err := repo.Migrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	tpl := &domainCampaign.Template{Name: "welcome", Body: "{Hi|Hey} {nombre}", Category: "marketing"}
	if err := repo.CreateTemplate(tpl); err != nil {
		t.Fatalf("create: %v", err)
	}
	if tpl.ID == 0 {
		t.Fatal("expected template id")
	}

	// name UNIQUE -> duplicate create must error.
	if err := repo.CreateTemplate(&domainCampaign.Template{Name: "welcome", Body: "x"}); err == nil {
		t.Fatal("expected unique-name violation")
	}

	tpl.Body = "updated"
	if err := repo.UpdateTemplate(tpl); err != nil {
		t.Fatalf("update: %v", err)
	}
	got, err := repo.GetTemplate(tpl.ID)
	if err != nil || got == nil || got.Body != "updated" {
		t.Fatalf("get after update: %v %+v", err, got)
	}

	list, err := repo.ListTemplates()
	if err != nil || len(list) != 1 {
		t.Fatalf("list: %v len=%d", err, len(list))
	}

	if err := repo.DeleteTemplate(tpl.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	got, _ = repo.GetTemplate(tpl.ID)
	if got != nil {
		t.Fatalf("expected nil after delete")
	}
}
