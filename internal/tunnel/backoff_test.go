package tunnel

import (
	"testing"
	"time"
)

func TestBackoffSequenceNoJitter(t *testing.T) {
	t.Parallel()
	b := Backoff{Base: time.Second, Max: 10 * time.Second, Factor: 2}
	want := []time.Duration{1 * time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second, 10 * time.Second, 10 * time.Second}
	for i, w := range want {
		got := b.Next()
		if got != w {
			t.Errorf("attempt %d: got %v want %v", i, got, w)
		}
	}
}

func TestBackoffReset(t *testing.T) {
	t.Parallel()
	b := Backoff{Base: time.Second, Max: 10 * time.Second, Factor: 2}
	b.Next()
	b.Next()
	if b.Attempt() != 2 {
		t.Fatalf("attempt = %d", b.Attempt())
	}
	b.Reset()
	if b.Attempt() != 0 {
		t.Fatalf("attempt after reset = %d", b.Attempt())
	}
	if got := b.Next(); got != time.Second {
		t.Errorf("first delay after reset = %v", got)
	}
}

func TestBackoffJitterBounded(t *testing.T) {
	t.Parallel()
	// Deterministic rand: always 0 -> maximum negative jitter (-50%).
	b := Backoff{Base: time.Second, Max: 10 * time.Second, Factor: 2, Jitter: 0.5, randFloat: func() float64 { return 0 }}
	got := b.Next()
	// base=1s, jitter delta = (0*2-1)*0.5*1s = -0.5s -> 0.5s
	if got != 500*time.Millisecond {
		t.Errorf("jittered delay = %v, want 500ms", got)
	}

	b2 := Backoff{Base: time.Second, Max: 10 * time.Second, Factor: 2, Jitter: 0.5, randFloat: func() float64 { return 1 }}
	got2 := b2.Next()
	// delta = (1*2-1)*0.5*1s = +0.5s -> 1.5s
	if got2 != 1500*time.Millisecond {
		t.Errorf("jittered delay = %v, want 1500ms", got2)
	}
}

func TestBackoffDefaults(t *testing.T) {
	t.Parallel()
	b := Backoff{} // all zero -> base 1s, factor 2, max 30s
	if got := b.Next(); got != time.Second {
		t.Errorf("default first delay = %v", got)
	}
}
