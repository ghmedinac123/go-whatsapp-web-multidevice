package campaign

import (
	"errors"
	"sync"
	"time"

	domainCampaign "github.com/aldinokemal/go-whatsapp-web-multidevice/domains/campaign"
)

// Health score tuning. A device starts at 1.0; each failure costs more than each
// success recovers, so a misbehaving number degrades quickly and is auto-disabled
// once it crosses the threshold.
const (
	HealthThreshold = 0.3  // below this a sender is auto-disabled
	healthIncrease  = 0.05 // recovery per successful delivery
	healthDecrease  = 0.2  // penalty per failure/timeout
	healthMax       = 1.0
	healthMin       = 0.0
)

// ErrNoSenderAvailable means no sender can send right now (all disabled or all hit
// their daily cap). Callers use EnabledCount to tell the two cases apart.
var ErrNoSenderAvailable = errors.New("no sender available")

// SenderPool rotates outbound messages across a campaign's sender devices using
// round-robin, while respecting each device's daily cap and health score. It is
// safe for concurrent use, though a campaign runner drives it from one goroutine.
type SenderPool struct {
	mu        sync.Mutex
	senders   []*domainCampaign.Sender
	nextIndex int
}

// NewSenderPool builds a pool over the given senders (typically loaded from the
// repository for one campaign). The slice is used as-is so mutations are visible
// to the caller for persistence.
func NewSenderPool(senders []*domainCampaign.Sender) *SenderPool {
	return &SenderPool{senders: senders}
}

// NextAvailable returns the next eligible sender in round-robin order, or
// ErrNoSenderAvailable when none can send right now.
func (p *SenderPool) NextAvailable() (*domainCampaign.Sender, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	n := len(p.senders)
	if n == 0 {
		return nil, ErrNoSenderAvailable
	}
	for i := 0; i < n; i++ {
		idx := (p.nextIndex + i) % n
		s := p.senders[idx]
		if isEligible(s) {
			p.nextIndex = (idx + 1) % n
			return s, nil
		}
	}
	return nil, ErrNoSenderAvailable
}

// ReportResult applies the outcome of a send to the matching sender: success
// bumps health and the daily counter; failure penalizes health and may
// auto-disable the device. It returns the affected sender so the caller can
// persist it, or nil when no sender matches deviceID.
func (p *SenderPool) ReportResult(deviceID string, success bool) *domainCampaign.Sender {
	p.mu.Lock()
	defer p.mu.Unlock()

	for _, s := range p.senders {
		if s.DeviceID != deviceID {
			continue
		}
		if success {
			s.HealthScore = clampFloat(s.HealthScore+healthIncrease, healthMin, healthMax)
			s.SentToday++
			now := time.Now().UTC()
			s.LastSentAt = &now
		} else {
			s.HealthScore = clampFloat(s.HealthScore-healthDecrease, healthMin, healthMax)
			if s.HealthScore < HealthThreshold {
				s.Enabled = false
			}
		}
		return s
	}
	return nil
}

// ResetDaily zeroes the per-day counter for every sender (midnight reset). Health
// and enabled state are intentionally preserved.
func (p *SenderPool) ResetDaily() {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, s := range p.senders {
		s.SentToday = 0
	}
}

// EnabledCount returns how many senders are enabled and above the health
// threshold, regardless of their daily cap. Zero means the pool is dead and the
// campaign cannot continue; a positive value with no NextAvailable means every
// usable sender has hit its daily cap.
func (p *SenderPool) EnabledCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	count := 0
	for _, s := range p.senders {
		if s.Enabled && s.HealthScore >= HealthThreshold {
			count++
		}
	}
	return count
}

// Senders returns a snapshot copy of the senders for stats/health dashboards.
func (p *SenderPool) Senders() []domainCampaign.Sender {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]domainCampaign.Sender, len(p.senders))
	for i, s := range p.senders {
		out[i] = *s
	}
	return out
}

func isEligible(s *domainCampaign.Sender) bool {
	return s.Enabled &&
		s.HealthScore >= HealthThreshold &&
		(s.MaxDaily <= 0 || s.SentToday < s.MaxDaily)
}

func clampFloat(v, min, max float64) float64 {
	if v < min {
		return min
	}
	if v > max {
		return max
	}
	return v
}
