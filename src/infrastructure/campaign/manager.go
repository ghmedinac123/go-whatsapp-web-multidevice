package campaign

import (
	"errors"
	"fmt"
	"sync"

	domainCampaign "github.com/aldinokemal/go-whatsapp-web-multidevice/domains/campaign"
)

var (
	// ErrCampaignNotFound is returned when a campaign id does not exist.
	ErrCampaignNotFound = errors.New("campaign not found")
	// ErrAlreadyRunning is returned when starting a campaign that is already running.
	ErrAlreadyRunning = errors.New("campaign already running")
	// ErrNotRunning is returned when pausing/cancelling a campaign that is not running.
	ErrNotRunning = errors.New("campaign not running")
	// ErrNoSenders is returned when starting a campaign without any sender device.
	ErrNoSenders = errors.New("campaign has no senders")
	// ErrNoPending is returned when starting a campaign with nothing left to send.
	ErrNoPending = errors.New("campaign has no pending recipients")
)

// Manager owns the lifecycle of running campaigns: it starts, pauses, resumes and
// cancels runners and keeps the campaign status column in sync. One runner exists
// per running campaign.
type Manager struct {
	mu      sync.Mutex
	repo    domainCampaign.ICampaignRepository
	sender  MessageSender
	runners map[int]*CampaignRunner
}

// NewManager builds a manager over the repository and message sender.
func NewManager(repo domainCampaign.ICampaignRepository, sender MessageSender) *Manager {
	return &Manager{
		repo:    repo,
		sender:  sender,
		runners: make(map[int]*CampaignRunner),
	}
}

// Start launches a campaign. It validates that the campaign exists, has senders
// and has pending recipients, then spins up a runner and marks it running.
func (m *Manager) Start(campaignID int) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, ok := m.runners[campaignID]; ok {
		return ErrAlreadyRunning
	}

	c, err := m.repo.GetCampaign(campaignID)
	if err != nil {
		return err
	}
	if c == nil {
		return ErrCampaignNotFound
	}

	senders, err := m.repo.ListSenders(campaignID)
	if err != nil {
		return err
	}
	if len(senders) == 0 {
		return ErrNoSenders
	}

	counts, err := m.repo.CountRecipientsByStatus(campaignID)
	if err != nil {
		return err
	}
	if counts[domainCampaign.RecipientPending] == 0 {
		return ErrNoPending
	}

	runner := newRunner(
		campaignID, c.TemplateBody, c.TemplateMedia,
		m.repo, m.sender, NewSenderPool(senders), DefaultDelayConfig(),
		m.remove,
	)
	if err := m.repo.SetCampaignStatus(campaignID, domainCampaign.StatusRunning); err != nil {
		return fmt.Errorf("set running: %w", err)
	}
	m.runners[campaignID] = runner
	runner.start()
	return nil
}

// Resume is an alias for Start that only applies to paused campaigns.
func (m *Manager) Resume(campaignID int) error {
	c, err := m.repo.GetCampaign(campaignID)
	if err != nil {
		return err
	}
	if c == nil {
		return ErrCampaignNotFound
	}
	if c.Status != domainCampaign.StatusPaused {
		return fmt.Errorf("campaign is %s, not paused", c.Status)
	}
	return m.Start(campaignID)
}

// Pause stops a running campaign and marks it paused. Pending recipients are left
// untouched so a later Resume continues from where it stopped.
func (m *Manager) Pause(campaignID int) error {
	return m.stopRunning(campaignID, domainCampaign.StatusPaused)
}

// Cancel stops a running campaign and marks it cancelled. If the campaign is not
// running it still flips a draft/scheduled/paused campaign to cancelled.
func (m *Manager) Cancel(campaignID int) error {
	if err := m.stopRunning(campaignID, domainCampaign.StatusCancelled); err == nil {
		return nil
	} else if !errors.Is(err, ErrNotRunning) {
		return err
	}
	// Not actively running: just flip the stored status.
	c, err := m.repo.GetCampaign(campaignID)
	if err != nil {
		return err
	}
	if c == nil {
		return ErrCampaignNotFound
	}
	return m.repo.SetCampaignStatus(campaignID, domainCampaign.StatusCancelled)
}

// stopRunning cancels the runner (if any) and sets the final status. It returns
// ErrNotRunning when the campaign has no active runner.
func (m *Manager) stopRunning(campaignID int, status string) error {
	m.mu.Lock()
	runner := m.runners[campaignID]
	delete(m.runners, campaignID)
	m.mu.Unlock()

	if runner == nil {
		return ErrNotRunning
	}
	runner.stop() // blocks until the goroutine exits
	return m.repo.SetCampaignStatus(campaignID, status)
}

// IsRunning reports whether a runner is currently active for the campaign.
func (m *Manager) IsRunning(campaignID int) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, ok := m.runners[campaignID]
	return ok
}

// remove drops a runner from the registry. It is the runner's onFinish callback,
// invoked when the goroutine exits on its own (completed / self-paused).
func (m *Manager) remove(campaignID int) {
	m.mu.Lock()
	delete(m.runners, campaignID)
	m.mu.Unlock()
}

// Stats returns the live progress snapshot of a campaign from recipient statuses
// plus the stored campaign status.
func (m *Manager) Stats(campaignID int) (domainCampaign.Stats, error) {
	c, err := m.repo.GetCampaign(campaignID)
	if err != nil {
		return domainCampaign.Stats{}, err
	}
	if c == nil {
		return domainCampaign.Stats{}, ErrCampaignNotFound
	}
	counts, err := m.repo.CountRecipientsByStatus(campaignID)
	if err != nil {
		return domainCampaign.Stats{}, err
	}

	stats := domainCampaign.Stats{
		CampaignID: campaignID,
		Status:     c.Status,
		Pending:    counts[domainCampaign.RecipientPending],
		Sent:       counts[domainCampaign.RecipientSent],
		Delivered:  counts[domainCampaign.RecipientDelivered],
		Read:       counts[domainCampaign.RecipientRead],
		Failed:     counts[domainCampaign.RecipientFailed],
		Replied:    counts[domainCampaign.RecipientReplied],
		Skipped:    counts[domainCampaign.RecipientSkipped],
	}
	for _, n := range counts {
		stats.Total += n
	}
	return stats, nil
}

// globalManager holds the process-wide campaign manager. It is set once at boot
// (cmd) and read by the REST handlers, matching the registry style used by the
// Chatwoot and webhook integrations.
var globalManager *Manager

// SetGlobalManager installs the process-wide campaign manager. Called once at boot.
func SetGlobalManager(m *Manager) { globalManager = m }

// GetGlobalManager returns the process-wide campaign manager, or nil if unset.
func GetGlobalManager() *Manager { return globalManager }
