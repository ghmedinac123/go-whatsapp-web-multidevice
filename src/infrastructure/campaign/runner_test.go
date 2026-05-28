package campaign

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	domainCampaign "github.com/aldinokemal/go-whatsapp-web-multidevice/domains/campaign"
)

// fakeSender records deliveries and can be told to fail specific phones.
type fakeSender struct {
	mu         sync.Mutex
	sent       []string
	failPhones map[string]bool
	composeN   int
}

func (f *fakeSender) SendText(_ context.Context, _, phone, _ string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failPhones[phone] {
		return "", errors.New("simulated send failure")
	}
	f.sent = append(f.sent, phone)
	return "msg-" + phone, nil
}

func (f *fakeSender) SendImageURL(_ context.Context, _, phone, _, _ string) (string, error) {
	return f.SendText(context.Background(), "", phone, "")
}

func (f *fakeSender) SendComposing(_ context.Context, _, _ string) error {
	f.mu.Lock()
	f.composeN++
	f.mu.Unlock()
	return nil
}

func (f *fakeSender) StopComposing(_ context.Context, _, _ string) error { return nil }

func (f *fakeSender) sentCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.sent)
}

func fastDelayConfig() DelayConfig {
	return DelayConfig{
		MinDelay:         time.Millisecond,
		MaxDelay:         2 * time.Millisecond,
		PauseEveryN:      1 << 30, // effectively never
		TypingMin:        time.Millisecond,
		TypingMax:        2 * time.Millisecond,
		ActiveHoursStart: 0,
		ActiveHoursEnd:   24, // always active
		Timezone:         "UTC",
	}
}

func seedCampaign(t *testing.T, repo *SQLiteRepository, devices []string, phones []string) int {
	t.Helper()
	c := &domainCampaign.Campaign{Name: "c", TemplateBody: "{Hola|Hey} {nombre}"}
	if err := repo.CreateCampaign(c); err != nil {
		t.Fatalf("create campaign: %v", err)
	}
	for _, d := range devices {
		if err := repo.AddSender(&domainCampaign.Sender{CampaignID: c.ID, DeviceID: d, Enabled: true}); err != nil {
			t.Fatalf("add sender: %v", err)
		}
	}
	recipients := make([]*domainCampaign.Recipient, len(phones))
	for i, p := range phones {
		recipients[i] = &domainCampaign.Recipient{Phone: p, Name: "Name" + p}
	}
	if _, err := repo.AddRecipients(c.ID, recipients, 0); err != nil {
		t.Fatalf("add recipients: %v", err)
	}
	return c.ID
}

func runUntilFinished(t *testing.T, runner *CampaignRunner, finished chan struct{}) {
	t.Helper()
	runner.start()
	select {
	case <-finished:
	case <-time.After(10 * time.Second):
		runner.stop()
		t.Fatal("runner did not finish in time")
	}
}

func TestRunnerSendsAllRecipients(t *testing.T) {
	repo := newTestRepo(t)
	id := seedCampaign(t, repo, []string{"dev-1", "dev-2"}, []string{"111", "222", "333"})

	fake := &fakeSender{}
	senders, _ := repo.ListSenders(id)
	finished := make(chan struct{})
	runner := newRunner(id, "{Hola|Hey} {nombre}", "", repo, fake, NewSenderPool(senders), fastDelayConfig(),
		func(int) { close(finished) })

	runUntilFinished(t, runner, finished)

	if fake.sentCount() != 3 {
		t.Fatalf("expected 3 sends, got %d", fake.sentCount())
	}
	counts, _ := repo.CountRecipientsByStatus(id)
	if counts[domainCampaign.RecipientSent] != 3 {
		t.Fatalf("expected 3 sent, got %+v", counts)
	}
	c, _ := repo.GetCampaign(id)
	if c.Status != domainCampaign.StatusCompleted {
		t.Fatalf("expected completed, got %q", c.Status)
	}

	// Sender stats must have been persisted (sent_today incremented).
	senders, _ = repo.ListSenders(id)
	total := 0
	for _, s := range senders {
		total += s.SentToday
	}
	if total != 3 {
		t.Fatalf("expected persisted sent_today total of 3, got %d", total)
	}
}

func TestRunnerMarksFailures(t *testing.T) {
	repo := newTestRepo(t)
	id := seedCampaign(t, repo, []string{"dev-1"}, []string{"111", "222", "333"})

	fake := &fakeSender{failPhones: map[string]bool{"222": true}}
	senders, _ := repo.ListSenders(id)
	finished := make(chan struct{})
	runner := newRunner(id, "hi {nombre}", "", repo, fake, NewSenderPool(senders), fastDelayConfig(),
		func(int) { close(finished) })

	runUntilFinished(t, runner, finished)

	counts, _ := repo.CountRecipientsByStatus(id)
	if counts[domainCampaign.RecipientSent] != 2 || counts[domainCampaign.RecipientFailed] != 1 {
		t.Fatalf("expected 2 sent / 1 failed, got %+v", counts)
	}
	c, _ := repo.GetCampaign(id)
	if c.Status != domainCampaign.StatusCompleted {
		t.Fatalf("expected completed despite a failure, got %q", c.Status)
	}
}

func TestManagerLifecycle(t *testing.T) {
	repo := newTestRepo(t)
	// Many recipients + default (slow) delays so the runner won't finish during the test.
	phones := make([]string, 50)
	for i := range phones {
		phones[i] = "5731000000" + string(rune('0'+i%10))
	}
	// Ensure uniqueness of phones.
	for i := range phones {
		phones[i] = phones[i] + "x" + time.Now().Format("150405") + itoa(i)
	}
	id := seedCampaign(t, repo, []string{"dev-1"}, phones)

	mgr := NewManager(repo, &fakeSender{})

	if err := mgr.Start(id); err != nil {
		t.Fatalf("start: %v", err)
	}
	if !mgr.IsRunning(id) {
		t.Fatal("expected campaign running after start")
	}
	if err := mgr.Start(id); err != ErrAlreadyRunning {
		t.Fatalf("expected ErrAlreadyRunning, got %v", err)
	}

	if err := mgr.Pause(id); err != nil {
		t.Fatalf("pause: %v", err)
	}
	if mgr.IsRunning(id) {
		t.Fatal("expected not running after pause")
	}
	if c, _ := repo.GetCampaign(id); c.Status != domainCampaign.StatusPaused {
		t.Fatalf("expected paused, got %q", c.Status)
	}

	if err := mgr.Resume(id); err != nil {
		t.Fatalf("resume: %v", err)
	}
	if !mgr.IsRunning(id) {
		t.Fatal("expected running after resume")
	}

	if err := mgr.Cancel(id); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	if c, _ := repo.GetCampaign(id); c.Status != domainCampaign.StatusCancelled {
		t.Fatalf("expected cancelled, got %q", c.Status)
	}
}

func TestManagerStartValidation(t *testing.T) {
	repo := newTestRepo(t)
	mgr := NewManager(repo, &fakeSender{})

	if err := mgr.Start(999); err != ErrCampaignNotFound {
		t.Fatalf("expected ErrCampaignNotFound, got %v", err)
	}

	c := &domainCampaign.Campaign{Name: "c", TemplateBody: "hi"}
	_ = repo.CreateCampaign(c)
	if err := mgr.Start(c.ID); err != ErrNoSenders {
		t.Fatalf("expected ErrNoSenders, got %v", err)
	}

	_ = repo.AddSender(&domainCampaign.Sender{CampaignID: c.ID, DeviceID: "dev-1", Enabled: true})
	if err := mgr.Start(c.ID); err != ErrNoPending {
		t.Fatalf("expected ErrNoPending, got %v", err)
	}
}

// itoa is a tiny dependency-free int->string for unique phone suffixes in tests.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}
