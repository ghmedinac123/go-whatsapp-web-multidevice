package campaign

// ICampaignRepository persists campaigns, their sender pool and recipients.
// The implementation lives in infrastructure/campaign (dependency inversion):
// the domain owns the contract, infrastructure owns the SQLite details.
type ICampaignRepository interface {
	// Migrate ensures the backing tables and indexes exist. Idempotent.
	Migrate() error

	// --- Campaigns ---
	// CreateCampaign inserts a new campaign and fills c.ID.
	CreateCampaign(c *Campaign) error
	// UpdateCampaign overwrites the mutable fields of an existing campaign.
	UpdateCampaign(c *Campaign) error
	// GetCampaign returns the campaign by id, or (nil, nil) when absent.
	GetCampaign(id int) (*Campaign, error)
	// ListCampaigns returns every campaign ordered by id descending (newest first).
	ListCampaigns() ([]*Campaign, error)
	// DeleteCampaign removes a campaign and its senders/recipients.
	DeleteCampaign(id int) error
	// SetCampaignStatus updates only the status column.
	SetCampaignStatus(id int, status string) error

	// --- Senders (number pool) ---
	// AddSender inserts a sender device for a campaign and fills s.ID.
	AddSender(s *Sender) error
	// UpdateSenderStats persists max_daily, health_score, sent_today, last_sent_at and enabled.
	UpdateSenderStats(s *Sender) error
	// ListSenders returns every sender bound to a campaign ordered by id.
	ListSenders(campaignID int) ([]*Sender, error)
	// DeleteSender removes a single sender by id.
	DeleteSender(id int) error
	// ResetDailySent zeroes sent_today for every sender of a campaign (midnight reset).
	ResetDailySent(campaignID int) error

	// --- Recipients ---
	// AddRecipients bulk-inserts recipients, ignoring duplicates by (campaign_id, phone).
	// When batchSize > 0 the inserted rows are split into lotes of that size
	// (continuing after any existing lote); otherwise they form a single new lote.
	// It returns the number of rows actually inserted.
	AddRecipients(campaignID int, recipients []*Recipient, batchSize int) (int, error)
	// ListRecipients returns recipients for a campaign. An empty status means "any";
	// a non-positive limit means "no limit".
	ListRecipients(campaignID int, status string, limit int) ([]*Recipient, error)
	// VariableKeys returns the distinct variable keys used across a campaign's
	// recipients (scanning up to limit rows), so the UI can show usable {tags}.
	VariableKeys(campaignID int, limit int) ([]string, error)
	// NextPendingRecipient returns the oldest pending recipient, or (nil, nil) when none remain.
	NextPendingRecipient(campaignID int) (*Recipient, error)
	// MarkRecipientSent flags a recipient as sent by a device, stamping sent_at.
	MarkRecipientSent(recipientID int, deviceID string) error
	// MarkRecipientFailed flags a recipient as failed with an error message.
	MarkRecipientFailed(recipientID int, errMsg string) error
	// CountRecipientsByStatus returns a status -> count map for a campaign.
	CountRecipientsByStatus(campaignID int) (map[string]int, error)
}

// ITemplateRepository persists reusable spintax templates.
type ITemplateRepository interface {
	// Migrate ensures the backing table exists. Idempotent.
	Migrate() error
	// CreateTemplate inserts a new template (name UNIQUE) and fills t.ID.
	CreateTemplate(t *Template) error
	// UpdateTemplate overwrites the mutable fields of an existing template.
	UpdateTemplate(t *Template) error
	// GetTemplate returns the template by id, or (nil, nil) when absent.
	GetTemplate(id int) (*Template, error)
	// ListTemplates returns every template ordered by name.
	ListTemplates() ([]*Template, error)
	// DeleteTemplate removes a template by id.
	DeleteTemplate(id int) error
}
