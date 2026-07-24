package herdr

import "encoding/json"

// request is an outbound socket frame. params is always a typed struct built by
// this package; browser input never reaches this field directly.
type request struct {
	ID     string `json:"id"`
	Method string `json:"method"`
	Params any    `json:"params"`
}

// envelope is a raw inbound frame. Exactly one of Result/Error is present on a
// well-formed response.
type envelope struct {
	ID     string          `json:"id"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *wireError      `json:"error,omitempty"`
}

type wireError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// resultType extracts the tagged discriminator shared by every success result.
type resultType struct {
	Type string `json:"type"`
}
