package campaign

import (
	"context"
	"strings"
	"time"

	domainCampaign "github.com/aldinokemal/go-whatsapp-web-multidevice/domains/campaign"
	"github.com/sirupsen/logrus"
)

// retryWhenStuck is how long the runner sleeps when every sender has hit its daily
// cap but the pool is otherwise healthy; it then re-checks (a day rollover resets
// the counters).
const retryWhenStuck = 15 * time.Minute

// CampaignRunner drives one campaign: it pulls pending recipients, rotates through
// the sender pool, generates a unique spintaxed message, simulates typing, sends,
// records the outcome, then waits a human-like delay before the next one.
//
// Lifecycle is owned by the Manager. Pause/Cancel cancel the context; the runner
// then exits without touching the campaign status (the Manager sets it). The
// runner only sets a terminal status itself when it finishes naturally
// (completed) or runs out of usable senders (paused).
type CampaignRunner struct {
	campaignID int
	template   string
	media      string

	repo    domainCampaign.ICampaignRepository
	sender  MessageSender
	pool    *SenderPool
	spintax *SpintaxEngine
	delay   *DelayEngine

	ctx      context.Context
	cancel   context.CancelFunc
	done     chan struct{}
	onFinish func(campaignID int)
}

// newRunner wires a runner. It does not start the goroutine; call start().
func newRunner(
	campaignID int,
	template, media string,
	repo domainCampaign.ICampaignRepository,
	sender MessageSender,
	pool *SenderPool,
	delayCfg DelayConfig,
	onFinish func(campaignID int),
) *CampaignRunner {
	ctx, cancel := context.WithCancel(context.Background())
	return &CampaignRunner{
		campaignID: campaignID,
		template:   template,
		media:      strings.TrimSpace(media),
		repo:       repo,
		sender:     sender,
		pool:       pool,
		spintax:    NewSpintaxEngine(),
		delay:      NewDelayEngine(delayCfg),
		ctx:        ctx,
		cancel:     cancel,
		done:       make(chan struct{}),
		onFinish:   onFinish,
	}
}

// start launches the processing goroutine.
func (r *CampaignRunner) start() {
	go r.run()
}

// stop signals the runner to exit and blocks until it has.
func (r *CampaignRunner) stop() {
	r.cancel()
	<-r.done
}

func (r *CampaignRunner) run() {
	defer close(r.done)
	if r.onFinish != nil {
		defer r.onFinish(r.campaignID)
	}

	currentDay := r.delay.Now().In(r.delay.loc).YearDay()

	for {
		if r.cancelled() {
			return
		}

		// Daily reset on day rollover (in the campaign timezone).
		if day := r.delay.Now().In(r.delay.loc).YearDay(); day != currentDay {
			currentDay = day
			r.pool.ResetDaily()
			if err := r.repo.ResetDailySent(r.campaignID); err != nil {
				logrus.Warnf("Campaign %d: daily reset failed: %v", r.campaignID, err)
			}
		}

		// Respect active hours.
		if wait := r.delay.DurationUntilActive(r.delay.Now()); wait > 0 {
			if !r.sleep(wait) {
				return
			}
			continue
		}

		rec, err := r.repo.NextPendingRecipient(r.campaignID)
		if err != nil {
			logrus.Warnf("Campaign %d: fetch recipient failed: %v", r.campaignID, err)
			if !r.sleep(5 * time.Second) {
				return
			}
			continue
		}
		if rec == nil {
			// No pending recipients left: the campaign is done.
			r.setStatus(domainCampaign.StatusCompleted)
			return
		}

		sender, err := r.pool.NextAvailable()
		if err != nil {
			if r.pool.EnabledCount() == 0 {
				// Every sender is disabled (health) — cannot continue, self-pause.
				logrus.Warnf("Campaign %d: no usable senders, pausing", r.campaignID)
				r.setStatus(domainCampaign.StatusPaused)
				return
			}
			// Senders exist but are capped for the day; wait and retry.
			if !r.sleep(retryWhenStuck) {
				return
			}
			continue
		}

		r.deliver(rec, sender)

		if !r.sleep(r.delay.NextDelay()) {
			return
		}
	}
}

// deliver generates the message, simulates typing, sends it and records the
// outcome against both the recipient and the sender pool.
func (r *CampaignRunner) deliver(rec *domainCampaign.Recipient, sender *domainCampaign.Sender) {
	message := r.spintax.Generate(r.template, buildVariables(rec))
	deviceID := sender.DeviceID

	// Presence simulation: "composing" for a length-proportional duration.
	if err := r.sender.SendComposing(r.ctx, deviceID, rec.Phone); err != nil {
		logrus.Debugf("Campaign %d: composing presence failed: %v", r.campaignID, err)
	}
	if !r.sleep(r.delay.TypingDelay(len(message))) {
		return
	}

	var sendErr error
	if r.media != "" {
		_, sendErr = r.sender.SendImageURL(r.ctx, deviceID, rec.Phone, message, r.media)
	} else {
		_, sendErr = r.sender.SendText(r.ctx, deviceID, rec.Phone, message)
	}

	if err := r.sender.StopComposing(r.ctx, deviceID, rec.Phone); err != nil {
		logrus.Debugf("Campaign %d: stop presence failed: %v", r.campaignID, err)
	}

	if sendErr != nil {
		logrus.Warnf("Campaign %d: send to %s via %s failed: %v", r.campaignID, rec.Phone, deviceID, sendErr)
		if err := r.repo.MarkRecipientFailed(rec.ID, sendErr.Error()); err != nil {
			logrus.Warnf("Campaign %d: mark failed error: %v", r.campaignID, err)
		}
	} else {
		if err := r.repo.MarkRecipientSent(rec.ID, deviceID); err != nil {
			logrus.Warnf("Campaign %d: mark sent error: %v", r.campaignID, err)
		}
	}

	if affected := r.pool.ReportResult(deviceID, sendErr == nil); affected != nil {
		if err := r.repo.UpdateSenderStats(affected); err != nil {
			logrus.Warnf("Campaign %d: persist sender stats failed: %v", r.campaignID, err)
		}
	}
}

func (r *CampaignRunner) setStatus(status string) {
	if err := r.repo.SetCampaignStatus(r.campaignID, status); err != nil {
		logrus.Warnf("Campaign %d: set status %s failed: %v", r.campaignID, status, err)
	}
}

func (r *CampaignRunner) cancelled() bool {
	select {
	case <-r.ctx.Done():
		return true
	default:
		return false
	}
}

// sleep waits for d or until the runner is cancelled. It returns false when
// cancelled (the caller should exit).
func (r *CampaignRunner) sleep(d time.Duration) bool {
	if d <= 0 {
		return !r.cancelled()
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-r.ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

// buildVariables merges the recipient's name and custom variables into the lookup
// the spintax engine uses. Name is exposed under both "nombre" and "name".
func buildVariables(rec *domainCampaign.Recipient) map[string]string {
	vars := make(map[string]string, len(rec.Variables)+3)
	for k, v := range rec.Variables {
		vars[k] = v
	}
	if rec.Name != "" {
		vars["nombre"] = rec.Name
		vars["name"] = rec.Name
	}
	vars["phone"] = rec.Phone
	return vars
}
