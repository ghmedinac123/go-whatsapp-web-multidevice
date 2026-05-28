package campaign

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	domainCampaign "github.com/aldinokemal/go-whatsapp-web-multidevice/domains/campaign"
)

// SQLiteRepository is a SQLite-backed implementation of
// domainCampaign.ICampaignRepository. It owns the campaigns, campaign_senders and
// campaign_recipients tables and creates them on demand via Migrate(), so it does
// not interfere with the chatstorage schema_info migration chain even when it
// shares the same *sql.DB connection.
type SQLiteRepository struct {
	db *sql.DB
}

// NewSQLiteRepository creates a repository over the given DB connection.
func NewSQLiteRepository(db *sql.DB) domainCampaign.ICampaignRepository {
	return &SQLiteRepository{db: db}
}

// Migrate creates the campaign tables and indexes if missing.
func (r *SQLiteRepository) Migrate() error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS campaigns (
			id             INTEGER PRIMARY KEY AUTOINCREMENT,
			name           TEXT    NOT NULL,
			status         TEXT    NOT NULL DEFAULT 'draft',
			template_body  TEXT    NOT NULL,
			template_media TEXT    NOT NULL DEFAULT '',
			schedule_at    DATETIME,
			created_at     DATETIME DEFAULT CURRENT_TIMESTAMP,
			updated_at     DATETIME DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE IF NOT EXISTS campaign_senders (
			id           INTEGER PRIMARY KEY AUTOINCREMENT,
			campaign_id  INTEGER NOT NULL,
			device_id    TEXT    NOT NULL,
			max_daily    INTEGER NOT NULL DEFAULT 200,
			health_score REAL    NOT NULL DEFAULT 1.0,
			sent_today   INTEGER NOT NULL DEFAULT 0,
			last_sent_at DATETIME,
			enabled      BOOLEAN NOT NULL DEFAULT 1
		)`,
		`CREATE INDEX IF NOT EXISTS idx_campaign_senders_campaign
			ON campaign_senders(campaign_id)`,
		`CREATE TABLE IF NOT EXISTS campaign_recipients (
			id             INTEGER PRIMARY KEY AUTOINCREMENT,
			campaign_id    INTEGER NOT NULL,
			phone          TEXT    NOT NULL,
			name           TEXT    NOT NULL DEFAULT '',
			variables      TEXT    NOT NULL DEFAULT '',
			status         TEXT    NOT NULL DEFAULT 'pending',
			sent_by_device TEXT    NOT NULL DEFAULT '',
			sent_at        DATETIME,
			delivered_at   DATETIME,
			read_at        DATETIME,
			replied_at     DATETIME,
			error_message  TEXT    NOT NULL DEFAULT '',
			batch          INTEGER NOT NULL DEFAULT 0,
			UNIQUE(campaign_id, phone)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_campaign_recipients_campaign_status
			ON campaign_recipients(campaign_id, status)`,
	}
	for _, stmt := range stmts {
		if _, err := r.db.Exec(stmt); err != nil {
			return fmt.Errorf("campaign migrate: %w", err)
		}
	}
	// Idempotent add for DBs created by an earlier build of this branch that
	// predates the batch column.
	if err := r.addColumnIfMissing("campaign_recipients", "batch", "INTEGER NOT NULL DEFAULT 0"); err != nil {
		return err
	}
	return nil
}

// addColumnIfMissing adds a column when it is absent, so re-running Migrate on an
// older DB is safe (SQLite has no "ADD COLUMN IF NOT EXISTS").
func (r *SQLiteRepository) addColumnIfMissing(table, column, ddl string) error {
	rows, err := r.db.Query("PRAGMA table_info(" + table + ")")
	if err != nil {
		return fmt.Errorf("campaign migrate inspect %s: %w", table, err)
	}
	defer rows.Close()
	for rows.Next() {
		var cid int
		var name, ctype string
		var notNull, pk int
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &ctype, &notNull, &dflt, &pk); err != nil {
			return fmt.Errorf("campaign migrate scan %s: %w", table, err)
		}
		if name == column {
			return nil // already present
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if _, err := r.db.Exec("ALTER TABLE " + table + " ADD COLUMN " + column + " " + ddl); err != nil {
		return fmt.Errorf("campaign migrate add column %s.%s: %w", table, column, err)
	}
	return nil
}

// --- Campaigns ---

func (r *SQLiteRepository) CreateCampaign(c *domainCampaign.Campaign) error {
	if c == nil || strings.TrimSpace(c.Name) == "" {
		return fmt.Errorf("campaign: name is required")
	}
	if strings.TrimSpace(c.TemplateBody) == "" {
		return fmt.Errorf("campaign: template_body is required")
	}
	if c.Status == "" {
		c.Status = domainCampaign.StatusDraft
	}
	res, err := r.db.Exec(`
		INSERT INTO campaigns (name, status, template_body, template_media, schedule_at)
		VALUES (?, ?, ?, ?, ?)
	`, c.Name, c.Status, c.TemplateBody, c.TemplateMedia, formatTime(c.ScheduleAt))
	if err != nil {
		return fmt.Errorf("campaign insert: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return fmt.Errorf("campaign insert id: %w", err)
	}
	c.ID = int(id)
	return nil
}

func (r *SQLiteRepository) UpdateCampaign(c *domainCampaign.Campaign) error {
	if c == nil || c.ID == 0 {
		return fmt.Errorf("campaign: id is required for update")
	}
	_, err := r.db.Exec(`
		UPDATE campaigns SET
			name           = ?,
			status         = ?,
			template_body  = ?,
			template_media = ?,
			schedule_at    = ?,
			updated_at     = CURRENT_TIMESTAMP
		WHERE id = ?
	`, c.Name, c.Status, c.TemplateBody, c.TemplateMedia, formatTime(c.ScheduleAt), c.ID)
	if err != nil {
		return fmt.Errorf("campaign update: %w", err)
	}
	return nil
}

func (r *SQLiteRepository) GetCampaign(id int) (*domainCampaign.Campaign, error) {
	row := r.db.QueryRow(`
		SELECT id, name, status, template_body, template_media, schedule_at, created_at, updated_at
		FROM campaigns WHERE id = ?
	`, id)

	c := &domainCampaign.Campaign{}
	var schedule, created, updated sql.NullString
	err := row.Scan(&c.ID, &c.Name, &c.Status, &c.TemplateBody, &c.TemplateMedia, &schedule, &created, &updated)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("campaign get: %w", err)
	}
	c.ScheduleAt = parseTimePtr(schedule)
	c.CreatedAt = parseTime(created)
	c.UpdatedAt = parseTime(updated)
	return c, nil
}

func (r *SQLiteRepository) ListCampaigns() ([]*domainCampaign.Campaign, error) {
	rows, err := r.db.Query(`
		SELECT id, name, status, template_body, template_media, schedule_at, created_at, updated_at
		FROM campaigns ORDER BY id DESC
	`)
	if err != nil {
		return nil, fmt.Errorf("campaign list: %w", err)
	}
	defer rows.Close()

	var out []*domainCampaign.Campaign
	for rows.Next() {
		c := &domainCampaign.Campaign{}
		var schedule, created, updated sql.NullString
		if err := rows.Scan(&c.ID, &c.Name, &c.Status, &c.TemplateBody, &c.TemplateMedia, &schedule, &created, &updated); err != nil {
			return nil, fmt.Errorf("campaign list scan: %w", err)
		}
		c.ScheduleAt = parseTimePtr(schedule)
		c.CreatedAt = parseTime(created)
		c.UpdatedAt = parseTime(updated)
		out = append(out, c)
	}
	return out, rows.Err()
}

func (r *SQLiteRepository) DeleteCampaign(id int) error {
	tx, err := r.db.Begin()
	if err != nil {
		return fmt.Errorf("campaign delete begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	for _, stmt := range []string{
		`DELETE FROM campaign_recipients WHERE campaign_id = ?`,
		`DELETE FROM campaign_senders WHERE campaign_id = ?`,
		`DELETE FROM campaigns WHERE id = ?`,
	} {
		if _, err := tx.Exec(stmt, id); err != nil {
			return fmt.Errorf("campaign delete: %w", err)
		}
	}
	return tx.Commit()
}

func (r *SQLiteRepository) SetCampaignStatus(id int, status string) error {
	_, err := r.db.Exec(`UPDATE campaigns SET status = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?`, status, id)
	if err != nil {
		return fmt.Errorf("campaign set status: %w", err)
	}
	return nil
}

// --- Senders ---

func (r *SQLiteRepository) AddSender(s *domainCampaign.Sender) error {
	if s == nil || s.CampaignID == 0 || strings.TrimSpace(s.DeviceID) == "" {
		return fmt.Errorf("campaign sender: campaign_id and device_id are required")
	}
	if s.MaxDaily <= 0 {
		s.MaxDaily = 200
	}
	if s.HealthScore == 0 {
		s.HealthScore = 1.0
	}
	res, err := r.db.Exec(`
		INSERT INTO campaign_senders (campaign_id, device_id, max_daily, health_score, sent_today, last_sent_at, enabled)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`, s.CampaignID, s.DeviceID, s.MaxDaily, s.HealthScore, s.SentToday, formatTime(s.LastSentAt), s.Enabled)
	if err != nil {
		return fmt.Errorf("campaign sender insert: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return fmt.Errorf("campaign sender insert id: %w", err)
	}
	s.ID = int(id)
	return nil
}

func (r *SQLiteRepository) UpdateSenderStats(s *domainCampaign.Sender) error {
	if s == nil || s.ID == 0 {
		return fmt.Errorf("campaign sender: id is required for update")
	}
	_, err := r.db.Exec(`
		UPDATE campaign_senders SET
			max_daily    = ?,
			health_score = ?,
			sent_today   = ?,
			last_sent_at = ?,
			enabled      = ?
		WHERE id = ?
	`, s.MaxDaily, s.HealthScore, s.SentToday, formatTime(s.LastSentAt), s.Enabled, s.ID)
	if err != nil {
		return fmt.Errorf("campaign sender update: %w", err)
	}
	return nil
}

func (r *SQLiteRepository) ListSenders(campaignID int) ([]*domainCampaign.Sender, error) {
	rows, err := r.db.Query(`
		SELECT id, campaign_id, device_id, max_daily, health_score, sent_today, last_sent_at, enabled
		FROM campaign_senders WHERE campaign_id = ? ORDER BY id
	`, campaignID)
	if err != nil {
		return nil, fmt.Errorf("campaign senders list: %w", err)
	}
	defer rows.Close()

	var out []*domainCampaign.Sender
	for rows.Next() {
		s := &domainCampaign.Sender{}
		var lastSent sql.NullString
		if err := rows.Scan(&s.ID, &s.CampaignID, &s.DeviceID, &s.MaxDaily, &s.HealthScore, &s.SentToday, &lastSent, &s.Enabled); err != nil {
			return nil, fmt.Errorf("campaign senders scan: %w", err)
		}
		s.LastSentAt = parseTimePtr(lastSent)
		out = append(out, s)
	}
	return out, rows.Err()
}

func (r *SQLiteRepository) DeleteSender(id int) error {
	if _, err := r.db.Exec(`DELETE FROM campaign_senders WHERE id = ?`, id); err != nil {
		return fmt.Errorf("campaign sender delete: %w", err)
	}
	return nil
}

func (r *SQLiteRepository) ResetDailySent(campaignID int) error {
	if _, err := r.db.Exec(`UPDATE campaign_senders SET sent_today = 0 WHERE campaign_id = ?`, campaignID); err != nil {
		return fmt.Errorf("campaign sender reset daily: %w", err)
	}
	return nil
}

// --- Recipients ---

func (r *SQLiteRepository) AddRecipients(campaignID int, recipients []*domainCampaign.Recipient, batchSize int) (int, error) {
	if campaignID == 0 {
		return 0, fmt.Errorf("campaign recipients: campaign_id is required")
	}
	if len(recipients) == 0 {
		return 0, nil
	}

	// Continue lote numbering after any existing lote for this campaign.
	var maxBatch int
	if err := r.db.QueryRow(`SELECT COALESCE(MAX(batch), 0) FROM campaign_recipients WHERE campaign_id = ?`, campaignID).Scan(&maxBatch); err != nil {
		return 0, fmt.Errorf("campaign recipients max batch: %w", err)
	}

	tx, err := r.db.Begin()
	if err != nil {
		return 0, fmt.Errorf("campaign recipients begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	stmt, err := tx.Prepare(`
		INSERT OR IGNORE INTO campaign_recipients (campaign_id, phone, name, variables, status, batch)
		VALUES (?, ?, ?, ?, 'pending', ?)
	`)
	if err != nil {
		return 0, fmt.Errorf("campaign recipients prepare: %w", err)
	}
	defer stmt.Close()

	added := 0
	for i, rec := range recipients {
		if rec == nil || strings.TrimSpace(rec.Phone) == "" {
			continue
		}
		vars, err := encodeVariables(rec.Variables)
		if err != nil {
			return added, fmt.Errorf("campaign recipients encode: %w", err)
		}
		batch := maxBatch + 1
		if batchSize > 0 {
			batch = maxBatch + 1 + (i / batchSize)
		}
		res, err := stmt.Exec(campaignID, rec.Phone, rec.Name, vars, batch)
		if err != nil {
			return added, fmt.Errorf("campaign recipients insert: %w", err)
		}
		if n, _ := res.RowsAffected(); n > 0 {
			added++
		}
	}
	if err := tx.Commit(); err != nil {
		return added, fmt.Errorf("campaign recipients commit: %w", err)
	}
	return added, nil
}

// VariableKeys scans up to `limit` recipients and returns the sorted distinct
// variable keys plus the always-available nombre/phone tags.
func (r *SQLiteRepository) VariableKeys(campaignID int, limit int) ([]string, error) {
	query := `SELECT variables FROM campaign_recipients WHERE campaign_id = ? AND variables != ''`
	args := []any{campaignID}
	if limit > 0 {
		query += ` LIMIT ?`
		args = append(args, limit)
	}
	rows, err := r.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("campaign variable keys: %w", err)
	}
	defer rows.Close()

	set := map[string]bool{"nombre": true, "phone": true}
	for rows.Next() {
		var vars string
		if err := rows.Scan(&vars); err != nil {
			return nil, fmt.Errorf("campaign variable keys scan: %w", err)
		}
		for k := range decodeVariables(vars) {
			set[k] = true
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	keys := make([]string, 0, len(set))
	for k := range set {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys, nil
}

func (r *SQLiteRepository) ListRecipients(campaignID int, status string, limit int) ([]*domainCampaign.Recipient, error) {
	query := `
		SELECT id, campaign_id, phone, name, variables, status, sent_by_device,
		       sent_at, delivered_at, read_at, replied_at, error_message, batch
		FROM campaign_recipients WHERE campaign_id = ?`
	args := []any{campaignID}
	if strings.TrimSpace(status) != "" {
		query += ` AND status = ?`
		args = append(args, status)
	}
	query += ` ORDER BY batch, id`
	if limit > 0 {
		query += ` LIMIT ?`
		args = append(args, limit)
	}

	rows, err := r.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("campaign recipients list: %w", err)
	}
	defer rows.Close()

	var out []*domainCampaign.Recipient
	for rows.Next() {
		rec, err := scanRecipient(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, rec)
	}
	return out, rows.Err()
}

func (r *SQLiteRepository) NextPendingRecipient(campaignID int) (*domainCampaign.Recipient, error) {
	row := r.db.QueryRow(`
		SELECT id, campaign_id, phone, name, variables, status, sent_by_device,
		       sent_at, delivered_at, read_at, replied_at, error_message, batch
		FROM campaign_recipients
		WHERE campaign_id = ? AND status = 'pending'
		ORDER BY batch, id LIMIT 1
	`, campaignID)

	rec, err := scanRecipientRow(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return rec, nil
}

func (r *SQLiteRepository) MarkRecipientSent(recipientID int, deviceID string) error {
	_, err := r.db.Exec(`
		UPDATE campaign_recipients
		SET status = 'sent', sent_by_device = ?, sent_at = CURRENT_TIMESTAMP, error_message = ''
		WHERE id = ?
	`, deviceID, recipientID)
	if err != nil {
		return fmt.Errorf("campaign recipient mark sent: %w", err)
	}
	return nil
}

func (r *SQLiteRepository) MarkRecipientFailed(recipientID int, errMsg string) error {
	_, err := r.db.Exec(`
		UPDATE campaign_recipients SET status = 'failed', error_message = ? WHERE id = ?
	`, errMsg, recipientID)
	if err != nil {
		return fmt.Errorf("campaign recipient mark failed: %w", err)
	}
	return nil
}

func (r *SQLiteRepository) CountRecipientsByStatus(campaignID int) (map[string]int, error) {
	rows, err := r.db.Query(`
		SELECT status, COUNT(*) FROM campaign_recipients WHERE campaign_id = ? GROUP BY status
	`, campaignID)
	if err != nil {
		return nil, fmt.Errorf("campaign recipients count: %w", err)
	}
	defer rows.Close()

	out := make(map[string]int)
	for rows.Next() {
		var status string
		var count int
		if err := rows.Scan(&status, &count); err != nil {
			return nil, fmt.Errorf("campaign recipients count scan: %w", err)
		}
		out[status] = count
	}
	return out, rows.Err()
}

// --- scan + encode helpers ---

// rowScanner abstracts *sql.Row and *sql.Rows so a single mapper serves both.
type rowScanner interface {
	Scan(dest ...any) error
}

func scanRecipient(rows *sql.Rows) (*domainCampaign.Recipient, error) {
	return scanRecipientRow(rows)
}

func scanRecipientRow(s rowScanner) (*domainCampaign.Recipient, error) {
	rec := &domainCampaign.Recipient{}
	var vars string
	var sentAt, deliveredAt, readAt, repliedAt sql.NullString
	err := s.Scan(
		&rec.ID, &rec.CampaignID, &rec.Phone, &rec.Name, &vars, &rec.Status, &rec.SentByDevice,
		&sentAt, &deliveredAt, &readAt, &repliedAt, &rec.ErrorMessage, &rec.Batch,
	)
	if err != nil {
		return nil, err
	}
	rec.Variables = decodeVariables(vars)
	rec.SentAt = parseTimePtr(sentAt)
	rec.DeliveredAt = parseTimePtr(deliveredAt)
	rec.ReadAt = parseTimePtr(readAt)
	rec.RepliedAt = parseTimePtr(repliedAt)
	return rec, nil
}

// encodeVariables marshals the variable map to the JSON TEXT column. A nil/empty
// map yields "" so the column stays clean.
func encodeVariables(vars map[string]string) (string, error) {
	if len(vars) == 0 {
		return "", nil
	}
	b, err := json.Marshal(vars)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// decodeVariables unmarshals the JSON TEXT column back into a map. An empty column
// yields a nil map.
func decodeVariables(vars string) map[string]string {
	if strings.TrimSpace(vars) == "" {
		return nil
	}
	out := make(map[string]string)
	if err := json.Unmarshal([]byte(vars), &out); err != nil {
		return nil
	}
	return out
}

// timeLayouts covers the SQLite CURRENT_TIMESTAMP format and the RFC3339 form we
// write ourselves, so scanning is robust across drivers.
var timeLayouts = []string{
	time.RFC3339Nano,
	time.RFC3339,
	"2006-01-02 15:04:05.999999999-07:00",
	"2006-01-02 15:04:05",
}

// formatTime renders a nullable time as the RFC3339 TEXT we store, or nil for NULL.
func formatTime(t *time.Time) any {
	if t == nil || t.IsZero() {
		return nil
	}
	return t.UTC().Format(time.RFC3339)
}

// parseTime parses a non-nullable timestamp column, returning the zero time when
// empty or unparseable.
func parseTime(ns sql.NullString) time.Time {
	if t := parseTimePtr(ns); t != nil {
		return *t
	}
	return time.Time{}
}

// parseTimePtr parses a nullable timestamp column, returning nil when empty.
func parseTimePtr(ns sql.NullString) *time.Time {
	if !ns.Valid || strings.TrimSpace(ns.String) == "" {
		return nil
	}
	for _, layout := range timeLayouts {
		if t, err := time.Parse(layout, ns.String); err == nil {
			utc := t.UTC()
			return &utc
		}
	}
	return nil
}
