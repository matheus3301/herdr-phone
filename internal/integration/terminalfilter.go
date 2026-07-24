package integration

import (
	"github.com/matheus3301/herdr-phone/internal/security"
	"github.com/matheus3301/herdr-phone/internal/terminal"
)

// ansiFilterAdapter presents the security package's streaming ANSI filter as a
// terminal.Filter. The server calls the TerminalFilterFactory once per terminal
// session, so each bridge owns a fresh filter and a fragmented escape sequence
// buffered by one controller can never bleed into a reconnect. Each instance is
// used by exactly one bridge goroutine, so no locking is needed.
type ansiFilterAdapter struct {
	f *security.ANSIFilter
}

var _ terminal.Filter = (*ansiFilterAdapter)(nil)

// newANSIFilter returns a fresh per-session filter.
func newANSIFilter() *ansiFilterAdapter {
	return &ansiFilterAdapter{f: security.NewANSIFilter()}
}

// FilterOutput sanitizes a controller output chunk. security.ANSIFilter.Filter
// returns a freshly allocated slice that never aliases src, satisfying the
// terminal.Filter retention contract.
func (a *ansiFilterAdapter) FilterOutput(src []byte) []byte {
	return a.f.Filter(src)
}
