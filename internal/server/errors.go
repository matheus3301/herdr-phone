package server

import (
	"encoding/json"
	"errors"
	"net/http"
)

// ErrPairing is returned by Authenticator.Pair for an invalid or already-used
// pairing secret. The server maps it to a 401 without leaking which.
var ErrPairing = errors.New("invalid or used pairing secret")

// API error codes. These are stable strings the frontend switches on.
const (
	codeUnauthorized        = "unauthorized"
	codeForbidden           = "forbidden"
	codeBadRequest          = "bad_request"
	codeNotFound            = "not_found"
	codeMethodNotAllowed    = "method_not_allowed"
	codeUnsupportedMedia    = "unsupported_media_type"
	codePayloadTooLarge     = "payload_too_large"
	codeRateLimited         = "rate_limited"
	codeConflict            = "conflict"
	codeGenerationStale     = "generation_stale"
	codeConfirmationNeeded  = "confirmation_required"
	codeConfirmationInvalid = "confirmation_invalid"
	codeDeadlineExceeded    = "deadline_exceeded"
	codeUnavailable         = "unavailable"
	codeInternal            = "internal"
)

// apiError is the body of a non-mutation error response.
type apiError struct {
	Error errBody `json:"error"`
}

type errBody struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	Retryable bool   `json:"retryable,omitempty"`
}

// writeError writes a JSON error with no-store headers. Messages are static and
// control-free so nothing sensitive leaks.
func writeError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(apiError{Error: errBody{Code: code, Message: message}})
}

// writeJSON writes a JSON value with the given status.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
