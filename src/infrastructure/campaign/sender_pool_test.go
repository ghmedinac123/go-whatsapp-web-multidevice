package campaign

import (
	"testing"

	domainCampaign "github.com/aldinokemal/go-whatsapp-web-multidevice/domains/campaign"
)

func newSender(device string, maxDaily int) *domainCampaign.Sender {
	return &domainCampaign.Sender{DeviceID: device, MaxDaily: maxDaily, HealthScore: 1.0, Enabled: true}
}

func TestPoolRoundRobin(t *testing.T) {
	pool := NewSenderPool([]*domainCampaign.Sender{
		newSender("a", 100), newSender("b", 100), newSender("c", 100),
	})
	order := []string{}
	for i := 0; i < 6; i++ {
		s, err := pool.NextAvailable()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		order = append(order, s.DeviceID)
	}
	want := []string{"a", "b", "c", "a", "b", "c"}
	for i := range want {
		if order[i] != want[i] {
			t.Fatalf("round-robin order = %v, want %v", order, want)
		}
	}
}

func TestPoolRespectsMaxDaily(t *testing.T) {
	pool := NewSenderPool([]*domainCampaign.Sender{newSender("a", 2)})
	for i := 0; i < 2; i++ {
		s, err := pool.NextAvailable()
		if err != nil {
			t.Fatalf("send %d: %v", i, err)
		}
		pool.ReportResult(s.DeviceID, true)
	}
	if _, err := pool.NextAvailable(); err != ErrNoSenderAvailable {
		t.Fatalf("expected ErrNoSenderAvailable after hitting cap, got %v", err)
	}

	// Daily reset makes it available again.
	pool.ResetDaily()
	if _, err := pool.NextAvailable(); err != nil {
		t.Fatalf("expected sender available after reset, got %v", err)
	}
}

func TestPoolHealthIncreaseOnSuccess(t *testing.T) {
	s := newSender("a", 100)
	s.HealthScore = 0.5
	pool := NewSenderPool([]*domainCampaign.Sender{s})
	pool.ReportResult("a", true)
	if s.HealthScore != 0.55 {
		t.Fatalf("expected health 0.55, got %v", s.HealthScore)
	}
	if s.SentToday != 1 {
		t.Fatalf("expected sent_today 1, got %d", s.SentToday)
	}
}

func TestPoolAutoDisableBelowThreshold(t *testing.T) {
	s := newSender("a", 100)
	pool := NewSenderPool([]*domainCampaign.Sender{s})

	// 1.0 -> 0.8 -> 0.6 -> 0.4 -> 0.2 (disabled at < 0.3, i.e. on the 4th failure).
	for i := 0; i < 4; i++ {
		pool.ReportResult("a", false)
	}
	if s.Enabled {
		t.Fatalf("expected sender disabled, health=%v", s.HealthScore)
	}
	if _, err := pool.NextAvailable(); err != ErrNoSenderAvailable {
		t.Fatalf("disabled sender should not be returned, got %v", err)
	}
	if pool.EnabledCount() != 0 {
		t.Fatalf("expected 0 enabled senders, got %d", pool.EnabledCount())
	}
}

func TestPoolEnabledCountVsCap(t *testing.T) {
	a := newSender("a", 1)
	pool := NewSenderPool([]*domainCampaign.Sender{a})
	s, _ := pool.NextAvailable()
	pool.ReportResult(s.DeviceID, true)

	// Capped for the day, but still enabled/healthy: EnabledCount > 0 yet
	// NextAvailable fails -> caller knows to wait for the daily reset, not abort.
	if pool.EnabledCount() != 1 {
		t.Fatalf("expected 1 enabled sender, got %d", pool.EnabledCount())
	}
	if _, err := pool.NextAvailable(); err != ErrNoSenderAvailable {
		t.Fatalf("expected no sender available (capped), got %v", err)
	}
}

func TestPoolEmpty(t *testing.T) {
	pool := NewSenderPool(nil)
	if _, err := pool.NextAvailable(); err != ErrNoSenderAvailable {
		t.Fatalf("expected ErrNoSenderAvailable for empty pool, got %v", err)
	}
}
