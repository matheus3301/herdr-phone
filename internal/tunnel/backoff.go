package tunnel

import (
	"math"
	"math/rand"
	"time"
)

// Backoff computes bounded exponential restart delays with optional jitter. It
// is not safe for concurrent use; callers hold it from a single supervise loop.
type Backoff struct {
	Base   time.Duration // delay for the first retry; must be > 0
	Max    time.Duration // ceiling for any single delay
	Factor float64       // growth per attempt; <=1 falls back to 2
	Jitter float64       // 0..1 fraction of the delay applied as +/- randomization

	// randFloat returns a value in [0,1). Injectable for deterministic tests.
	randFloat func() float64

	attempt int
}

func (b *Backoff) base() time.Duration {
	if b.Base <= 0 {
		return time.Second
	}
	return b.Base
}

func (b *Backoff) factor() float64 {
	if b.Factor <= 1 {
		return 2
	}
	return b.Factor
}

func (b *Backoff) max() time.Duration {
	if b.Max <= 0 {
		return 30 * time.Second
	}
	return b.Max
}

func (b *Backoff) rnd() float64 {
	if b.randFloat != nil {
		return b.randFloat()
	}
	return rand.Float64()
}

// Next returns the delay for the current attempt and advances the counter.
func (b *Backoff) Next() time.Duration {
	exp := math.Pow(b.factor(), float64(b.attempt))
	b.attempt++

	delay := float64(b.base()) * exp
	if maxNs := float64(b.max()); delay > maxNs {
		delay = maxNs
	}

	if b.Jitter > 0 {
		frac := b.Jitter
		if frac > 1 {
			frac = 1
		}
		// Symmetric jitter in [-frac, +frac].
		delta := (b.rnd()*2 - 1) * frac * delay
		delay += delta
		if delay < 0 {
			delay = 0
		}
	}

	d := time.Duration(delay)
	if capD := b.max(); d > capD {
		d = capD
	}
	return d
}

// Reset returns the backoff to its initial attempt count.
func (b *Backoff) Reset() {
	b.attempt = 0
}

// Attempt reports how many delays have been produced since the last Reset.
func (b *Backoff) Attempt() int {
	return b.attempt
}
