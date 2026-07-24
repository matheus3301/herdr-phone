package terminal

import "time"

// MessageType identifies a WebSocket data message reaching the bridge.
type MessageType int

const (
	// MessageText carries a typed JSON control command from the browser.
	MessageText MessageType = 1
	// MessageBinary carries raw terminal input bytes from the browser.
	MessageBinary MessageType = 2
)

// Conn is the browser-facing WebSocket the bridge drives. It is satisfied by an
// adapter around github.com/coder/websocket (see internal/server) in production
// and by an in-memory fake in tests, so the bridge needs no real network or
// browser.
//
// The bridge guarantees exactly one goroutine calls the write methods
// (WriteBinary/WriteText/Ping) and exactly one goroutine calls ReadMessage, so
// implementations need only be safe for that single-reader/single-writer split.
type Conn interface {
	// ReadMessage blocks for the next browser message, honoring the read
	// deadline. It returns a non-nil error when the connection is closed.
	ReadMessage() (MessageType, []byte, error)
	// WriteBinary sends filtered terminal output bytes.
	WriteBinary([]byte) error
	// WriteText sends a JSON metadata/lifecycle message.
	WriteText([]byte) error
	// Ping sends a WebSocket ping for liveness.
	Ping([]byte) error
	// SetReadDeadline bounds how long a read may block before the pong timeout
	// trips.
	SetReadDeadline(time.Time) error
	// SetWriteDeadline bounds a single write, providing write backpressure.
	SetWriteDeadline(time.Time) error
	// Close sends a close frame with code/reason and tears down the transport.
	Close(code int, reason string) error
}

// Close codes forwarded to the browser (RFC 6455 subset), defined here so the
// bridge stays independent of any concrete WebSocket library.
const (
	closeNormal        = 1000
	closeGoingAway     = 1001
	closePolicy        = 1008
	closeInternalError = 1011
)
