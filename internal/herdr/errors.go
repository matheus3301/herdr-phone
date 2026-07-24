package herdr

import (
	"errors"
	"fmt"
	"strings"
)

// Local error codes used for transport and client-side failures. Server-side
// codes (for example "not_found", "invalid_params", "feature_disabled") are
// preserved verbatim from Herdr.
const (
	CodeConnect       = "connect"         // could not dial the socket
	CodeTransport     = "transport"       // read/write failed mid-request
	CodeTimeout       = "timeout"         // deadline elapsed before a response
	CodeCanceled      = "canceled"        // context canceled before a response
	CodeProtocol      = "protocol"        // malformed frame or mismatched id
	CodeUnexpectedTyp = "unexpected_type" // result discriminator did not match
	CodeFrameTooLarge = "frame_too_large" // response exceeded the byte bound
	CodeIncompatible  = "incompatible"    // server protocol/version rejected
)

// Error is a structured Herdr error. Its message is bounded and stripped of
// control characters so it is safe to surface and to log.
type Error struct {
	Code    string
	Message string
}

func (e *Error) Error() string {
	if e.Message == "" {
		return e.Code
	}
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

// IsCode reports whether err is a *Error with the given code.
func IsCode(err error, code string) bool {
	var he *Error
	return errors.As(err, &he) && he.Code == code
}

// maxErrorMessage bounds any message we keep from Herdr or transport errors.
const maxErrorMessage = 512

// newError builds a sanitized, bounded *Error.
func newError(code, msg string) *Error {
	return &Error{Code: code, Message: sanitizeMessage(msg)}
}

// sanitizeMessage removes control characters (including ANSI escape
// introducers) and bounds length, so error text cannot inject terminal
// sequences into logs or UI.
func sanitizeMessage(s string) string {
	if len(s) > maxErrorMessage {
		s = s[:maxErrorMessage]
	}
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch {
		case r == '\t' || r == ' ':
			b.WriteRune(r)
		case r < 0x20 || r == 0x7f:
			// drop C0 controls and DEL, including ESC (0x1b)
		case r >= 0x80 && r <= 0x9f:
			// drop C1 controls
		default:
			b.WriteRune(r)
		}
	}
	return strings.TrimSpace(b.String())
}
