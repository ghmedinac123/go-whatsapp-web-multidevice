package campaign

import "time"

// Campaign lifecycle status values. A campaign moves draft|scheduled -> running
// -> (paused -> running)* -> completed, and may be cancelled from any state.
const (
	StatusDraft     = "draft"
	StatusScheduled = "scheduled"
	StatusRunning   = "running"
	StatusPaused    = "paused"
	StatusCompleted = "completed"
	StatusCancelled = "cancelled"
)

// Recipient delivery status values. pending -> sent -> delivered -> read, with
// replied set when the recipient answers and failed/skipped as terminal states.
const (
	RecipientPending   = "pending"
	RecipientSent      = "sent"
	RecipientDelivered = "delivered"
	RecipientRead      = "read"
	RecipientFailed    = "failed"
	RecipientReplied   = "replied"
	RecipientSkipped   = "skipped"
)

// Campaign is a mass-messaging job: one spintax template body (plus optional
// media) delivered to many recipients through a pool of sender devices.
type Campaign struct {
	ID            int        `json:"id"`
	Name          string     `json:"name"`
	Status        string     `json:"status"`
	TemplateBody  string     `json:"template_body"`
	TemplateMedia string     `json:"template_media"`
	ScheduleAt    *time.Time `json:"schedule_at"`
	CreatedAt     time.Time  `json:"created_at"`
	UpdatedAt     time.Time  `json:"updated_at"`
}

// Sender binds one WhatsApp device (JID) to a campaign as a sending number. The
// pool rotates across enabled senders, respecting MaxDaily and HealthScore.
type Sender struct {
	ID          int        `json:"id"`
	CampaignID  int        `json:"campaign_id"`
	DeviceID    string     `json:"device_id"`
	MaxDaily    int        `json:"max_daily"`
	HealthScore float64    `json:"health_score"`
	SentToday   int        `json:"sent_today"`
	LastSentAt  *time.Time `json:"last_sent_at"`
	Enabled     bool       `json:"enabled"`
}

// Recipient is a single target of a campaign. Variables feed spintax placeholders
// like {empresa}; Name feeds {nombre}. UNIQUE(campaign_id, phone) dedupes imports.
type Recipient struct {
	ID           int               `json:"id"`
	CampaignID   int               `json:"campaign_id"`
	Phone        string            `json:"phone"`
	Name         string            `json:"name"`
	Variables    map[string]string `json:"variables"`
	Status       string            `json:"status"`
	SentByDevice string            `json:"sent_by_device"`
	SentAt       *time.Time        `json:"sent_at"`
	DeliveredAt  *time.Time        `json:"delivered_at"`
	ReadAt       *time.Time        `json:"read_at"`
	RepliedAt    *time.Time        `json:"replied_at"`
	ErrorMessage string            `json:"error_message"`
}

// Template is a reusable, named spintax body with optional media and category.
type Template struct {
	ID        int       `json:"id"`
	Name      string    `json:"name"`
	Body      string    `json:"body"`
	MediaURL  string    `json:"media_url"`
	Category  string    `json:"category"`
	CreatedAt time.Time `json:"created_at"`
}

// Stats is the real-time progress snapshot of a campaign, derived from recipient
// statuses plus the in-flight runner status.
type Stats struct {
	CampaignID int    `json:"campaign_id"`
	Status     string `json:"status"`
	Total      int    `json:"total"`
	Pending    int    `json:"pending"`
	Sent       int    `json:"sent"`
	Delivered  int    `json:"delivered"`
	Read       int    `json:"read"`
	Failed     int    `json:"failed"`
	Replied    int    `json:"replied"`
	Skipped    int    `json:"skipped"`
}
