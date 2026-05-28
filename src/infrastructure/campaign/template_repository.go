package campaign

import (
	"database/sql"
	"fmt"
	"strings"

	domainCampaign "github.com/aldinokemal/go-whatsapp-web-multidevice/domains/campaign"
)

// TemplateRepository is a SQLite-backed implementation of
// domainCampaign.ITemplateRepository, owning the campaign_templates table.
type TemplateRepository struct {
	db *sql.DB
}

// NewTemplateRepository creates a repository over the given DB connection.
func NewTemplateRepository(db *sql.DB) domainCampaign.ITemplateRepository {
	return &TemplateRepository{db: db}
}

// Migrate creates the campaign_templates table if missing.
func (r *TemplateRepository) Migrate() error {
	stmt := `CREATE TABLE IF NOT EXISTS campaign_templates (
		id         INTEGER PRIMARY KEY AUTOINCREMENT,
		name       TEXT NOT NULL UNIQUE,
		body       TEXT NOT NULL,
		media_url  TEXT NOT NULL DEFAULT '',
		category   TEXT NOT NULL DEFAULT '',
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP
	)`
	if _, err := r.db.Exec(stmt); err != nil {
		return fmt.Errorf("campaign template migrate: %w", err)
	}
	return nil
}

func (r *TemplateRepository) CreateTemplate(t *domainCampaign.Template) error {
	if t == nil || strings.TrimSpace(t.Name) == "" {
		return fmt.Errorf("campaign template: name is required")
	}
	if strings.TrimSpace(t.Body) == "" {
		return fmt.Errorf("campaign template: body is required")
	}
	res, err := r.db.Exec(`
		INSERT INTO campaign_templates (name, body, media_url, category)
		VALUES (?, ?, ?, ?)
	`, t.Name, t.Body, t.MediaURL, t.Category)
	if err != nil {
		return fmt.Errorf("campaign template insert: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return fmt.Errorf("campaign template insert id: %w", err)
	}
	t.ID = int(id)
	return nil
}

func (r *TemplateRepository) UpdateTemplate(t *domainCampaign.Template) error {
	if t == nil || t.ID == 0 {
		return fmt.Errorf("campaign template: id is required for update")
	}
	_, err := r.db.Exec(`
		UPDATE campaign_templates SET name = ?, body = ?, media_url = ?, category = ? WHERE id = ?
	`, t.Name, t.Body, t.MediaURL, t.Category, t.ID)
	if err != nil {
		return fmt.Errorf("campaign template update: %w", err)
	}
	return nil
}

func (r *TemplateRepository) GetTemplate(id int) (*domainCampaign.Template, error) {
	row := r.db.QueryRow(`
		SELECT id, name, body, media_url, category, created_at FROM campaign_templates WHERE id = ?
	`, id)
	t := &domainCampaign.Template{}
	var created sql.NullString
	err := row.Scan(&t.ID, &t.Name, &t.Body, &t.MediaURL, &t.Category, &created)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("campaign template get: %w", err)
	}
	t.CreatedAt = parseTime(created)
	return t, nil
}

func (r *TemplateRepository) ListTemplates() ([]*domainCampaign.Template, error) {
	rows, err := r.db.Query(`
		SELECT id, name, body, media_url, category, created_at FROM campaign_templates ORDER BY name
	`)
	if err != nil {
		return nil, fmt.Errorf("campaign template list: %w", err)
	}
	defer rows.Close()

	var out []*domainCampaign.Template
	for rows.Next() {
		t := &domainCampaign.Template{}
		var created sql.NullString
		if err := rows.Scan(&t.ID, &t.Name, &t.Body, &t.MediaURL, &t.Category, &created); err != nil {
			return nil, fmt.Errorf("campaign template scan: %w", err)
		}
		t.CreatedAt = parseTime(created)
		out = append(out, t)
	}
	return out, rows.Err()
}

func (r *TemplateRepository) DeleteTemplate(id int) error {
	if _, err := r.db.Exec(`DELETE FROM campaign_templates WHERE id = ?`, id); err != nil {
		return fmt.Errorf("campaign template delete: %w", err)
	}
	return nil
}
