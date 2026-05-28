package campaign

import (
	"testing"
	"time"
)

func TestNextDelayWithinBounds(t *testing.T) {
	e := NewDelayEngine(DelayConfig{
		MinDelay:    15 * time.Second,
		MaxDelay:    45 * time.Second,
		PauseEveryN: 50,
		PauseMin:    10 * time.Minute,
		PauseMax:    15 * time.Minute,
	})
	for i := 1; i <= 500; i++ {
		d := e.NextDelay()
		if i%50 == 0 {
			if d < 10*time.Minute || d > 15*time.Minute {
				t.Fatalf("pause at i=%d out of [10m,15m]: %v", i, d)
			}
		} else {
			if d < 15*time.Second || d > 45*time.Second {
				t.Fatalf("delay at i=%d out of [15s,45s]: %v", i, d)
			}
		}
	}
}

func TestNextDelayHasVariation(t *testing.T) {
	// Gaussian + jitter must not produce a constant value (detectable pattern).
	e := NewDelayEngine(DelayConfig{MinDelay: 15 * time.Second, MaxDelay: 45 * time.Second})
	first := e.NextDelay()
	distinct := false
	for i := 0; i < 50; i++ {
		if e.NextDelay() != first {
			distinct = true
			break
		}
	}
	if !distinct {
		t.Fatal("expected delays to vary, got a constant value")
	}
}

func TestTypingDelayScalesWithLength(t *testing.T) {
	e := NewDelayEngine(DelayConfig{})
	for _, n := range []int{0, 50, 200, 1000} {
		d := e.TypingDelay(n)
		if d < 2*time.Second || d > 5*time.Second {
			t.Fatalf("typing delay for len=%d out of [2s,5s]: %v", n, d)
		}
	}
}

func TestIsActiveHours(t *testing.T) {
	e := NewDelayEngine(DelayConfig{ActiveHoursStart: 8, ActiveHoursEnd: 20, Timezone: "UTC"})
	cases := []struct {
		hour int
		want bool
	}{
		{0, false}, {7, false}, {8, true}, {12, true}, {19, true}, {20, false}, {23, false},
	}
	for _, c := range cases {
		at := time.Date(2026, 5, 28, c.hour, 0, 0, 0, time.UTC)
		if got := e.IsActive(at); got != c.want {
			t.Fatalf("IsActive(%02d:00) = %v, want %v", c.hour, got, c.want)
		}
	}
}

func TestDurationUntilActive(t *testing.T) {
	e := NewDelayEngine(DelayConfig{ActiveHoursStart: 8, ActiveHoursEnd: 20, Timezone: "UTC"})

	// Inside the window -> no wait.
	inside := time.Date(2026, 5, 28, 10, 0, 0, 0, time.UTC)
	if d := e.DurationUntilActive(inside); d != 0 {
		t.Fatalf("expected 0 inside window, got %v", d)
	}

	// Before the window -> wait until today 08:00.
	early := time.Date(2026, 5, 28, 3, 0, 0, 0, time.UTC)
	if d := e.DurationUntilActive(early); d != 5*time.Hour {
		t.Fatalf("expected 5h wait, got %v", d)
	}

	// After the window -> wait until tomorrow 08:00.
	late := time.Date(2026, 5, 28, 21, 0, 0, 0, time.UTC)
	if d := e.DurationUntilActive(late); d != 11*time.Hour {
		t.Fatalf("expected 11h wait, got %v", d)
	}
}

func TestUnknownTimezoneFallsBackToUTC(t *testing.T) {
	e := NewDelayEngine(DelayConfig{Timezone: "Mars/Olympus"})
	if e.loc != time.UTC {
		t.Fatalf("expected UTC fallback, got %v", e.loc)
	}
}
