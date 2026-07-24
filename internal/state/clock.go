package state

import "time"

// Clock abstracts time so the poll cadence is deterministic under test.
// Production code uses [SystemClock].
type Clock interface {
	Now() time.Time
	NewTicker(d time.Duration) Ticker
}

// Ticker is a resettable ticker, mirroring the subset of *time.Ticker the
// engine needs.
type Ticker interface {
	C() <-chan time.Time
	Reset(d time.Duration)
	Stop()
}

type systemClock struct{}

func (systemClock) Now() time.Time { return time.Now() }

func (systemClock) NewTicker(d time.Duration) Ticker {
	return &systemTicker{t: time.NewTicker(d)}
}

type systemTicker struct{ t *time.Ticker }

func (s *systemTicker) C() <-chan time.Time   { return s.t.C }
func (s *systemTicker) Reset(d time.Duration) { s.t.Reset(d) }
func (s *systemTicker) Stop()                 { s.t.Stop() }

// SystemClock is the production [Clock].
var SystemClock Clock = systemClock{}
