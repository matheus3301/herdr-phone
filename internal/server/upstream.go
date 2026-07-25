package server

import "errors"

// UpstreamCoder is implemented by an error that carries a structured upstream
// (Herdr) failure code. Injected collaborators return errors satisfying it so
// the relay can keep genuinely different failures apart — a vanished resource, a
// disabled feature, a transport fault, a timeout — instead of flattening every
// operation failure into one opaque code.
//
// Declaring the contract here rather than importing the Herdr package keeps the
// server dependent only on injected interfaces. The upstream *message* is never
// consulted: a Herdr error message can quote pane content, so only the code
// crosses this boundary and only static messages leave the relay.
type UpstreamCoder interface {
	UpstreamCode() string
}

// Upstream failure codes this relay recognizes. The first group is Herdr's
// server-side error set (protocol 17); the second is the Herdr adapter's own
// transport codes. Anything outside both sets is treated as an unclassified
// upstream fault.
const (
	upstreamNotFound            = "not_found"
	upstreamInvalidParams       = "invalid_params"
	upstreamInvalidRequest      = "invalid_request"
	upstreamFeatureDisabled     = "feature_disabled"
	upstreamPlatformUnsupported = "platform_unsupported"
	upstreamPluginDisabled      = "plugin_disabled"
	upstreamStreamConflict      = "stream_conflict"

	upstreamConnect      = "connect"
	upstreamTransport    = "transport"
	upstreamTimeout      = "timeout"
	upstreamCanceled     = "canceled"
	upstreamIncompatible = "incompatible"
)

// upstreamCode extracts the structured upstream code from err, or "" when err
// carries none.
func upstreamCode(err error) string {
	var uc UpstreamCoder
	if errors.As(err, &uc) {
		return uc.UpstreamCode()
	}
	return ""
}
