package terminal

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
)

// Record types on the Herdr controller's stdout stream (section 3.1 of SPEC.md).
const (
	recordFrame  = "terminal.frame"
	recordClosed = "terminal.closed"
)

// Command types written to the Herdr controller's stdin (section 3.1).
const (
	cmdInput   = "terminal.input"
	cmdResize  = "terminal.resize"
	cmdScroll  = "terminal.scroll"
	cmdRelease = "terminal.release"
)

// controllerRecord is one NDJSON line emitted by `herdr terminal session
// control`. Unknown fields are tolerated by encoding/json, matching the spec
// requirement to tolerate unknown response fields.
type controllerRecord struct {
	Type     string `json:"type"`
	Seq      uint64 `json:"seq"`
	Encoding string `json:"encoding"`
	Width    int    `json:"width"`
	Height   int    `json:"height"`
	Full     bool   `json:"full"`
	Bytes    string `json:"bytes"`
	Reason   string `json:"reason"`
}

// decodeBytes base64-decodes the frame payload.
func (r *controllerRecord) decodeBytes() ([]byte, error) {
	if r.Bytes == "" {
		return nil, nil
	}
	return base64.StdEncoding.DecodeString(r.Bytes)
}

// controllerCommand is one NDJSON line written to the controller's stdin.
type controllerCommand struct {
	Type string `json:"type"`
	// input
	Text  string `json:"text,omitempty"`
	Bytes string `json:"bytes,omitempty"`
	// resize
	Cols         int `json:"cols,omitempty"`
	Rows         int `json:"rows,omitempty"`
	CellWidthPx  int `json:"cell_width_px,omitempty"`
	CellHeightPx int `json:"cell_height_px,omitempty"`
	// scroll
	Direction string `json:"direction,omitempty"`
	Lines     int    `json:"lines,omitempty"`
	Source    string `json:"source,omitempty"`
}

func inputBytesCommand(b []byte) controllerCommand {
	return controllerCommand{Type: cmdInput, Bytes: base64.StdEncoding.EncodeToString(b)}
}

func resizeCommand(cols, rows, cellW, cellH int) controllerCommand {
	return controllerCommand{Type: cmdResize, Cols: cols, Rows: rows, CellWidthPx: cellW, CellHeightPx: cellH}
}

func scrollCommand(direction string, lines int, source string) controllerCommand {
	return controllerCommand{Type: cmdScroll, Direction: direction, Lines: lines, Source: source}
}

func releaseCommand() controllerCommand { return controllerCommand{Type: cmdRelease} }

// browserCommand is a text (JSON) message sent by the browser over the terminal
// WebSocket. Binary browser messages are raw terminal input bytes and are not
// decoded through this type.
type browserCommand struct {
	Type         string `json:"type"`
	Cols         int    `json:"cols"`
	Rows         int    `json:"rows"`
	CellWidthPx  int    `json:"cell_width_px"`
	CellHeightPx int    `json:"cell_height_px"`
	Direction    string `json:"direction"`
	Lines        int    `json:"lines"`
	Source       string `json:"source"`
}

// Browser command type strings.
const (
	browserResize  = "resize"
	browserScroll  = "scroll"
	browserRelease = "release"
	browserPing    = "ping"
)

func parseBrowserCommand(data []byte) (browserCommand, error) {
	var c browserCommand
	if err := json.Unmarshal(data, &c); err != nil {
		return c, fmt.Errorf("terminal: bad browser command: %w", err)
	}
	if c.Type == "" {
		return c, errors.New("terminal: browser command missing type")
	}
	return c, nil
}

// serverMessage is a text (JSON) message sent to the browser to convey terminal
// metadata or lifecycle. Terminal *content* is always sent as binary frames and
// never travels through this type.
type serverMessage struct {
	Type   string `json:"type"`
	Reason string `json:"reason,omitempty"`
	Width  int    `json:"width,omitempty"`
	Height int    `json:"height,omitempty"`
	Full   bool   `json:"full,omitempty"`
	Seq    uint64 `json:"seq,omitempty"`
}

// Server message type strings.
const (
	msgOpened   = "terminal.opened"
	msgClosed   = "terminal.closed"
	msgConflict = "terminal.conflict"
	msgPong     = "terminal.pong"
	msgResized  = "terminal.resized"
)

func (m serverMessage) encode() []byte {
	b, _ := json.Marshal(m)
	return b
}
