package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/matheus3301/herdr-phone/internal/herdr"
)

// agentKindsTTL bounds how often startable agent kinds are re-discovered. A fresh
// value is reused for this long so neither the capabilities document nor
// agent-start validation spawns a `herdr agent` subprocess on every request.
const agentKindsTTL = 60 * time.Second

// kindsSource discovers the agent kinds this Herdr build can start. *herdr.Client
// satisfies it via StartableAgentKinds.
type kindsSource interface {
	StartableAgentKinds(ctx context.Context) ([]string, error)
}

// agentKinds is a bounded, single-flight-ish cache of authoritative startable
// agent kinds. It refreshes at most once per TTL, serializes concurrent refreshes
// under its mutex, and briefly serves the last good value if a refresh fails —
// but it never invents a compiled-in list, so a genuine discovery outage surfaces
// as an error to callers.
type agentKinds struct {
	src kindsSource
	ttl time.Duration
	now func() time.Time

	mu        sync.Mutex
	cached    []string
	fetchedAt time.Time
	loaded    bool
}

func newAgentKinds(src kindsSource, ttl time.Duration, now func() time.Time) *agentKinds {
	if now == nil {
		now = time.Now
	}
	return &agentKinds{src: src, ttl: ttl, now: now}
}

// list returns the current startable kinds, refreshing when the cache is cold or
// stale. On a refresh failure it returns the last good value if it is at most one
// extra TTL old; otherwise it returns the discovery error.
func (a *agentKinds) list(ctx context.Context) ([]string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.loaded && a.now().Sub(a.fetchedAt) < a.ttl {
		return a.cached, nil
	}

	kinds, err := a.src.StartableAgentKinds(ctx)
	if err != nil {
		if a.loaded && a.now().Sub(a.fetchedAt) <= 2*a.ttl {
			return a.cached, nil
		}
		return nil, err
	}
	a.cached = kinds
	a.fetchedAt = a.now()
	a.loaded = true
	return kinds, nil
}

// validate confirms kind is among the authoritatively discovered kinds. It fails
// closed: an unknown kind, or a discovery outage that leaves no usable list, both
// return an error so an agent is never started against a stale or guessed set.
func (a *agentKinds) validate(ctx context.Context, kind string) error {
	if kind == "" {
		return herdr.NewError(herdr.CodeInvalidParams, "agent.start requires a kind")
	}
	kinds, err := a.list(ctx)
	if err != nil {
		return fmt.Errorf("integration: cannot validate agent kind: %w", err)
	}
	for _, k := range kinds {
		if k == kind {
			return nil
		}
	}
	return herdr.NewError(herdr.CodeInvalidParams, fmt.Sprintf("agent kind %q is not one this herdr build can start", kind))
}

// capabilitiesBase holds the non-kind capability fields learned at handshake.
type capabilitiesBase struct {
	HerdrVersion  string
	HerdrProtocol int
	LiveHandoff   bool
}

// capabilitiesJSON renders the capabilities document: the handshake facts plus
// the authoritative startable agent kinds. When discovery is currently failing,
// it omits the kinds and records a bounded error string instead of a stale or
// invented list, so the frontend disables agent-start rather than guessing.
func (b capabilitiesBase) capabilitiesJSON(ctx context.Context, kinds *agentKinds) json.RawMessage {
	doc := map[string]any{
		"herdr_version":  b.HerdrVersion,
		"herdr_protocol": b.HerdrProtocol,
		"live_handoff":   b.LiveHandoff,
	}
	if list, err := kinds.list(ctx); err != nil {
		doc["agent_kinds_error"] = "agent kinds unavailable"
	} else {
		if list == nil {
			list = []string{}
		}
		doc["agent_kinds"] = list
	}
	out, err := json.Marshal(doc)
	if err != nil {
		return json.RawMessage(`{}`)
	}
	return out
}
