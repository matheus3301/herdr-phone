package integration

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"
)

// fakeKindsSource is a controllable kindsSource for cache tests. It counts calls
// so cache/refresh behaviour can be asserted.
type fakeKindsSource struct {
	mu    sync.Mutex
	kinds []string
	err   error
	calls int
}

func (f *fakeKindsSource) StartableAgentKinds(context.Context) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if f.err != nil {
		return nil, f.err
	}
	return append([]string(nil), f.kinds...), nil
}

func (f *fakeKindsSource) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

func (f *fakeKindsSource) set(kinds []string, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.kinds, f.err = kinds, err
}

func TestAgentKindsCachesWithinTTL(t *testing.T) {
	t.Parallel()
	now := time.Now()
	clock := func() time.Time { return now }
	src := &fakeKindsSource{kinds: []string{"claude", "codex"}}
	ak := newAgentKinds(src, time.Minute, clock)

	for i := 0; i < 3; i++ {
		got, err := ak.list(context.Background())
		if err != nil || len(got) != 2 {
			t.Fatalf("list: %v %v", got, err)
		}
	}
	if src.callCount() != 1 {
		t.Errorf("expected 1 discovery within TTL, got %d", src.callCount())
	}

	// Advance past the TTL: the next list refreshes.
	now = now.Add(2 * time.Minute)
	if _, err := ak.list(context.Background()); err != nil {
		t.Fatal(err)
	}
	if src.callCount() != 2 {
		t.Errorf("expected a refresh after TTL, got %d calls", src.callCount())
	}
}

func TestAgentKindsServesStaleThenFailsClosed(t *testing.T) {
	t.Parallel()
	now := time.Now()
	clock := func() time.Time { return now }
	src := &fakeKindsSource{kinds: []string{"claude"}}
	ak := newAgentKinds(src, time.Minute, clock)

	if _, err := ak.list(context.Background()); err != nil {
		t.Fatal(err)
	}

	// Discovery starts failing. Just past the TTL, the last good value is served.
	src.set(nil, errors.New("herdr down"))
	now = now.Add(90 * time.Second) // within 2*TTL of the last good fetch
	got, err := ak.list(context.Background())
	if err != nil || len(got) != 1 {
		t.Errorf("expected stale value served, got %v %v", got, err)
	}

	// Well past 2*TTL, it fails closed rather than serving an ancient list.
	now = now.Add(3 * time.Minute)
	if _, err := ak.list(context.Background()); err == nil {
		t.Error("expected fail-closed error once the cache is too old")
	}
}

func TestAgentKindsValidate(t *testing.T) {
	t.Parallel()
	ak := newAgentKinds(&fakeKindsSource{kinds: []string{"claude", "codex"}}, time.Minute, time.Now)
	if err := ak.validate(context.Background(), "claude"); err != nil {
		t.Errorf("valid kind rejected: %v", err)
	}
	if err := ak.validate(context.Background(), "totally-made-up"); err == nil {
		t.Error("unknown kind must be rejected")
	}
	if err := ak.validate(context.Background(), ""); err == nil {
		t.Error("empty kind must be rejected")
	}
}

func TestAgentKindsValidateFailsClosedOnDiscoveryError(t *testing.T) {
	t.Parallel()
	ak := newAgentKinds(&fakeKindsSource{err: errors.New("no kinds")}, time.Minute, time.Now)
	if err := ak.validate(context.Background(), "claude"); err == nil {
		t.Error("validate must fail closed when discovery fails")
	}
}

func TestCapabilitiesJSONIncludesKinds(t *testing.T) {
	t.Parallel()
	base := capabilitiesBase{HerdrVersion: "0.7.5", HerdrProtocol: 17, LiveHandoff: true}

	ok := base.capabilitiesJSON(context.Background(), newAgentKinds(&fakeKindsSource{kinds: []string{"claude"}}, time.Minute, time.Now))
	var doc map[string]any
	if err := json.Unmarshal(ok, &doc); err != nil {
		t.Fatal(err)
	}
	if _, has := doc["agent_kinds"]; !has {
		t.Errorf("capabilities missing agent_kinds: %s", ok)
	}
	if _, has := doc["agent_kinds_error"]; has {
		t.Errorf("healthy discovery must not set agent_kinds_error: %s", ok)
	}

	bad := base.capabilitiesJSON(context.Background(), newAgentKinds(&fakeKindsSource{err: errors.New("down")}, time.Minute, time.Now))
	doc = map[string]any{}
	if err := json.Unmarshal(bad, &doc); err != nil {
		t.Fatal(err)
	}
	if _, has := doc["agent_kinds"]; has {
		t.Errorf("failed discovery must not emit a kinds list: %s", bad)
	}
	if _, has := doc["agent_kinds_error"]; !has {
		t.Errorf("failed discovery must record agent_kinds_error: %s", bad)
	}
}

// TestMutatorValidatesAgentStartKind proves agent.start is rejected before any
// Herdr call when the kind is not authoritatively startable, and allowed when it
// is.
func TestMutatorValidatesAgentStartKind(t *testing.T) {
	t.Parallel()
	f := startFakeHerdr(t)
	client := dispatchClientFor(f)
	m := &mutatorAdapter{client: client, kinds: newAgentKinds(&fakeKindsSource{kinds: []string{"claude"}}, time.Minute, time.Now)}

	// Unknown kind: rejected, and Herdr is never called.
	if _, err := m.Mutate(context.Background(), "agent.start", json.RawMessage(`{"pane_id":"p1","kind":"bogus","name":"a1"}`)); err == nil {
		t.Fatal("expected rejection of unknown kind")
	}
	if f.params("agent.start") != nil {
		t.Error("Herdr agent.start must not be called for an invalid kind")
	}

	// Known kind: dispatched to Herdr.
	if _, err := m.Mutate(context.Background(), "agent.start", json.RawMessage(`{"pane_id":"p1","kind":"claude","name":"a1"}`)); err != nil {
		t.Fatalf("valid kind should dispatch: %v", err)
	}
	if f.params("agent.start") == nil {
		t.Error("Herdr agent.start should have been called for a valid kind")
	}
}
