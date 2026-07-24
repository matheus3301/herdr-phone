package herdr

import "time"

// Clock abstracts the wall clock so request timeouts and subscriber backoff are
// deterministic under test. Production code uses [SystemClock].
type Clock interface {
	// Now returns the current time.
	Now() time.Time
	// After returns a channel that receives once after d elapses. A d <= 0
	// yields a channel that fires immediately.
	After(d time.Duration) <-chan time.Time
}

type systemClock struct{}

func (systemClock) Now() time.Time { return time.Now() }

func (systemClock) After(d time.Duration) <-chan time.Time {
	if d <= 0 {
		ch := make(chan time.Time, 1)
		ch <- time.Now()
		return ch
	}
	return time.After(d)
}

// SystemClock is the production [Clock] backed by the time package.
var SystemClock Clock = systemClock{}
