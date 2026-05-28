package campaign

import (
	"math/rand"
	"sync"
	"time"
)

// DelayConfig tunes the human-like pacing of a campaign. Zero values are filled
// with DefaultDelayConfig() by NewDelayEngine, so partial configs are safe.
type DelayConfig struct {
	MinDelay         time.Duration `json:"min_delay"`          // shortest gap between messages
	MaxDelay         time.Duration `json:"max_delay"`          // longest gap between messages
	PauseEveryN      int           `json:"pause_every_n"`      // take a long break after this many messages
	PauseMin         time.Duration `json:"pause_min"`          // shortest long break
	PauseMax         time.Duration `json:"pause_max"`          // longest long break
	ActiveHoursStart int           `json:"active_hours_start"` // inclusive hour [0-23]
	ActiveHoursEnd   int           `json:"active_hours_end"`   // exclusive hour [1-24]
	Timezone         string        `json:"timezone"`           // IANA tz, e.g. "America/Bogota"
	TypingMin        time.Duration `json:"typing_min"`         // shortest "composing" duration
	TypingMax        time.Duration `json:"typing_max"`         // longest "composing" duration
}

// DefaultDelayConfig returns conservative anti-ban defaults: 15-45s between
// messages, a 10-15min break every 50 messages, and an 8am-8pm window in Bogotá.
func DefaultDelayConfig() DelayConfig {
	return DelayConfig{
		MinDelay:         15 * time.Second,
		MaxDelay:         45 * time.Second,
		PauseEveryN:      50,
		PauseMin:         10 * time.Minute,
		PauseMax:         15 * time.Minute,
		ActiveHoursStart: 8,
		ActiveHoursEnd:   20,
		Timezone:         "America/Bogota",
		TypingMin:        2 * time.Second,
		TypingMax:        5 * time.Second,
	}
}

// withDefaults fills any zero field from DefaultDelayConfig.
func (c DelayConfig) withDefaults() DelayConfig {
	d := DefaultDelayConfig()
	if c.MinDelay > 0 {
		d.MinDelay = c.MinDelay
	}
	if c.MaxDelay > 0 {
		d.MaxDelay = c.MaxDelay
	}
	if c.MaxDelay < c.MinDelay {
		d.MaxDelay = d.MinDelay
	}
	if c.PauseEveryN > 0 {
		d.PauseEveryN = c.PauseEveryN
	}
	if c.PauseMin > 0 {
		d.PauseMin = c.PauseMin
	}
	if c.PauseMax > 0 {
		d.PauseMax = c.PauseMax
	}
	if c.ActiveHoursStart >= 0 && c.ActiveHoursStart <= 23 {
		d.ActiveHoursStart = c.ActiveHoursStart
	}
	if c.ActiveHoursEnd >= 1 && c.ActiveHoursEnd <= 24 {
		d.ActiveHoursEnd = c.ActiveHoursEnd
	}
	if c.Timezone != "" {
		d.Timezone = c.Timezone
	}
	if c.TypingMin > 0 {
		d.TypingMin = c.TypingMin
	}
	if c.TypingMax > 0 {
		d.TypingMax = c.TypingMax
	}
	if d.TypingMax < d.TypingMin {
		d.TypingMax = d.TypingMin
	}
	return d
}

// DelayEngine produces randomized, human-looking delays and decides whether the
// current moment falls inside the campaign's active hours.
type DelayEngine struct {
	cfg DelayConfig
	loc *time.Location

	mu    sync.Mutex
	rng   *rand.Rand
	count int

	// now is injectable so tests can pin the clock.
	now func() time.Time
}

// NewDelayEngine builds an engine from cfg (defaults applied). The timezone is
// resolved once; an unknown tz falls back to UTC so the engine never fails.
func NewDelayEngine(cfg DelayConfig) *DelayEngine {
	cfg = cfg.withDefaults()
	loc, err := time.LoadLocation(cfg.Timezone)
	if err != nil || loc == nil {
		loc = time.UTC
	}
	return &DelayEngine{
		cfg: cfg,
		loc: loc,
		rng: rand.New(rand.NewSource(time.Now().UnixNano())),
		now: time.Now,
	}
}

// Config returns the effective configuration after defaults were applied.
func (e *DelayEngine) Config() DelayConfig { return e.cfg }

// NextDelay returns the gap to wait before the next message. Every PauseEveryN
// messages it returns a long break instead of the normal short gap.
func (e *DelayEngine) NextDelay() time.Duration {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.count++
	if e.cfg.PauseEveryN > 0 && e.count%e.cfg.PauseEveryN == 0 {
		return e.randDuration(e.cfg.PauseMin, e.cfg.PauseMax)
	}
	return e.gaussianDelay()
}

// TypingDelay returns how long to show the "composing" indicator before sending,
// 2-5s scaled by message length with light jitter, so longer texts "take longer
// to type".
func (e *DelayEngine) TypingDelay(messageLen int) time.Duration {
	const fullAtChars = 200.0
	minTyping, maxTyping := e.cfg.TypingMin, e.cfg.TypingMax

	ratio := float64(messageLen) / fullAtChars
	if ratio > 1 {
		ratio = 1
	}
	if ratio < 0 {
		ratio = 0
	}
	base := minTyping + time.Duration(ratio*float64(maxTyping-minTyping))

	e.mu.Lock()
	jitter := (e.rng.Float64()*0.3 - 0.15) // +-15%
	e.mu.Unlock()

	d := time.Duration(float64(base) * (1 + jitter))
	return clampDuration(d, minTyping, maxTyping)
}

// IsActive reports whether t (in the engine's timezone) is inside active hours.
func (e *DelayEngine) IsActive(t time.Time) bool {
	h := t.In(e.loc).Hour()
	start, end := e.cfg.ActiveHoursStart, e.cfg.ActiveHoursEnd
	if start == end {
		return true // full day
	}
	if start < end {
		return h >= start && h < end
	}
	// Overnight window (e.g. 20 -> 6).
	return h >= start || h < end
}

// DurationUntilActive returns 0 when t is already active, otherwise the wait until
// the next active-window start.
func (e *DelayEngine) DurationUntilActive(t time.Time) time.Duration {
	lt := t.In(e.loc)
	if e.IsActive(lt) {
		return 0
	}
	next := time.Date(lt.Year(), lt.Month(), lt.Day(), e.cfg.ActiveHoursStart, 0, 0, 0, e.loc)
	if !next.After(lt) {
		next = next.AddDate(0, 0, 1)
	}
	return next.Sub(lt)
}

// Now returns the engine's current time (respects the injected clock in tests).
func (e *DelayEngine) Now() time.Time { return e.now() }

func (e *DelayEngine) gaussianDelay() time.Duration {
	min, max := e.cfg.MinDelay, e.cfg.MaxDelay
	if max <= min {
		return min
	}
	mean := float64(min+max) / 2
	std := float64(max-min) / 4 // ~95% of draws land inside [min,max] before clamping
	v := e.rng.NormFloat64()*std + mean
	return clampDuration(time.Duration(v), min, max)
}

func (e *DelayEngine) randDuration(min, max time.Duration) time.Duration {
	if max <= min {
		return min
	}
	return min + time.Duration(e.rng.Int63n(int64(max-min)))
}

func clampDuration(d, min, max time.Duration) time.Duration {
	if d < min {
		return min
	}
	if d > max {
		return max
	}
	return d
}
