package herdr

import (
	"context"
	"strings"
)

// Capabilities are the optional server features advertised by ping.
type Capabilities struct {
	LiveHandoff          bool `json:"live_handoff"`
	DetachedServerDaemon bool `json:"detached_server_daemon"`
}

// Pong is the ping handshake result.
type Pong struct {
	Type         string       `json:"type"`
	Version      string       `json:"version"`
	Protocol     int          `json:"protocol"`
	Capabilities Capabilities `json:"capabilities"`
}

// Ping performs the handshake and returns the raw pong. It does not enforce
// compatibility; use [Client.Handshake] for that.
func (c *Client) Ping(ctx context.Context) (*Pong, error) {
	var p Pong
	if err := c.call(ctx, "ping", struct{}{}, "pong", &p); err != nil {
		return nil, err
	}
	return &p, nil
}

// Handshake pings and verifies protocol compatibility. It tolerates unknown
// response fields (they are ignored) but rejects a mismatched protocol so an
// incompatible server surfaces clearly rather than corrupting later decodes.
func (c *Client) Handshake(ctx context.Context) (*Pong, error) {
	p, err := c.Ping(ctx)
	if err != nil {
		return nil, err
	}
	if p.Protocol != Protocol {
		return nil, newError(CodeIncompatible,
			"herdr protocol "+itoa(p.Protocol)+" is not supported; require "+itoa(Protocol))
	}
	// Validate the reported version too (SPEC §10), but only reject builds
	// strictly older than the minimum. Newer patch/minor/major releases are
	// accepted — the protocol number is the authoritative wire gate, and an
	// unparseable version string is left to that gate rather than rejected.
	if older, ok := versionOlderThan(p.Version, MinHerdrVersion); ok && older {
		return nil, newError(CodeIncompatible,
			"herdr version "+sanitizeMessage(p.Version)+" is older than the required "+MinHerdrVersion)
	}
	return p, nil
}

// versionOlderThan reports whether semantic version v is strictly older than
// min, comparing major.minor.patch. ok is false when either version cannot be
// parsed, in which case the caller must not reject on version alone.
func versionOlderThan(v, min string) (older, ok bool) {
	vv, okv := parseVersion(v)
	mm, okm := parseVersion(min)
	if !okv || !okm {
		return false, false
	}
	for i := range 3 {
		if vv[i] != mm[i] {
			return vv[i] < mm[i], true
		}
	}
	return false, true // equal
}

// parseVersion parses a leading "major.minor.patch" (ignoring any -pre/+build
// suffix and a leading 'v'). Missing components default to 0.
func parseVersion(s string) ([3]int, bool) {
	var out [3]int
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "v")
	if s == "" {
		return out, false
	}
	// Trim any pre-release / build metadata.
	if i := strings.IndexAny(s, "-+"); i >= 0 {
		s = s[:i]
	}
	parts := strings.Split(s, ".")
	if len(parts) > 3 {
		return out, false
	}
	for i, part := range parts {
		if part == "" {
			return out, false
		}
		n := 0
		for _, r := range part {
			if r < '0' || r > '9' {
				return out, false
			}
			n = n*10 + int(r-'0')
		}
		out[i] = n
	}
	return out, true
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}
