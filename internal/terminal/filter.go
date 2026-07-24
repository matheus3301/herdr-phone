package terminal

// Filter sanitizes terminal output before it reaches the browser or any log.
//
// The concrete, security-reviewed implementation (stripping OSC 52, OSC 8,
// title set/report, DCS/APC/PM, device status queries, and answerback
// sequences while preserving SGR, cursor control, erasure, alternate-screen,
// UTF-8 text, and mouse input) lives in the internal/security package and is
// injected per session so it can carry state across fragmented escape
// sequences. This package only defines the contract and applies it, keeping the
// bridge free of a hard dependency on the security package.
type Filter interface {
	// FilterOutput returns a sanitized copy of a terminal output chunk. The
	// returned slice must be safe for the caller to retain; implementations must
	// not alias src. A stateful filter may buffer an incomplete trailing escape
	// sequence and emit it on a later call.
	FilterOutput(src []byte) []byte
}

// NopFilter passes output through unchanged. It exists so the bridge and its
// tests can run without the security package. Production wiring must inject the
// real filter; a nil filter defaults to NopFilter.
type NopFilter struct{}

// FilterOutput returns a copy of src unchanged.
func (NopFilter) FilterOutput(src []byte) []byte {
	if len(src) == 0 {
		return nil
	}
	out := make([]byte, len(src))
	copy(out, src)
	return out
}
