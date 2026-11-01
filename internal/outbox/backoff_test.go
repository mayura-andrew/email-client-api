package outbox

import (
	"errors"
	"testing"
	"time"
)

func TestBackoffGrowsAndCaps(t *testing.T) {
	for attempt, base := range map[int]time.Duration{1: 30 * time.Second, 2: time.Minute, 3: 2 * time.Minute, 20: 30 * time.Minute} {
		for i := 0; i < 50; i++ {
			got := Backoff(attempt)
			if got < base*8/10 || got > base*12/10 {
				t.Fatalf("Backoff(%d) = %v, want within 20%% of %v", attempt, got, base)
			}
		}
	}
}

func TestErrorClassification(t *testing.T) {
	base := errors.New("boom")
	if !IsPermanent(Permanent(base)) || IsPermanent(base) {
		t.Fatal("Permanent wrapper not detected")
	}
	until := time.Now().Add(time.Hour)
	got, ok := deferredUntil(Defer(base, until))
	if !ok || !got.Equal(until) {
		t.Fatal("Defer wrapper not detected")
	}
	if !errors.Is(Permanent(base), base) {
		t.Fatal("wrappers must unwrap to the cause")
	}
}
