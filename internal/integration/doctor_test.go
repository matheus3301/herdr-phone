package integration

import (
	"strings"
	"testing"

	"github.com/matheus3301/herdr-phone/internal/config"
)

// namedAccessConfig builds a named-mode config with the given identity gate.
func namedAccessConfig(allowed []string, allowAny bool) config.Config {
	return config.Config{
		Cloudflare: config.Cloudflare{Mode: config.ModeNamed, PublicURL: "https://phone.example.com"},
		Access: config.Access{
			Enabled:           true,
			TeamDomain:        "team.cloudflareaccess.com",
			Audience:          "aud",
			AllowedIdentities: allowed,
			AllowAnyIdentity:  allowAny,
		},
	}
}

func TestAccessIdentityGateCheckReportsAllowlist(t *testing.T) {
	t.Parallel()
	c, ok := accessIdentityGateCheck(namedAccessConfig([]string{"op@example.com", "svc"}, false))
	if !ok {
		t.Fatal("named mode must produce an identity-gate check")
	}
	if c.Name != accessIdentityGateName || !c.OK {
		t.Fatalf("check = %+v, want a passing %q", c, accessIdentityGateName)
	}
	if !strings.Contains(c.Detail, "2 identity") {
		t.Errorf("detail should report the count: %q", c.Detail)
	}
	// The identities are never printed: doctor output lands in bug reports and panes.
	if strings.Contains(c.Detail, "op@example.com") || strings.Contains(c.Detail, "svc") {
		t.Errorf("detail must not name the allowed identities: %q", c.Detail)
	}
}

// TestAccessIdentityGateCheckWarnsOnOptOut is the point of the check: a
// deliberately wide-open named mode must be stated loudly on every doctor run,
// not pass silently.
func TestAccessIdentityGateCheckWarnsOnOptOut(t *testing.T) {
	t.Parallel()
	c, ok := accessIdentityGateCheck(namedAccessConfig(nil, true))
	if !ok {
		t.Fatal("named mode must produce an identity-gate check")
	}
	if !c.OK {
		t.Error("a declared opt-out is a valid configuration, so the check must not report failure")
	}
	if !strings.HasPrefix(c.Detail, "WARNING:") {
		t.Errorf("the opt-out state must be surfaced as a warning: %q", c.Detail)
	}
	for _, want := range []string{"allow_any_identity", "allowed_identities"} {
		if !strings.Contains(c.Detail, want) {
			t.Errorf("warning must mention %q: %q", want, c.Detail)
		}
	}
	// A whitespace-only entry is not an allowlist, so it must warn too rather than
	// read as "2 identities allowed".
	blank, _ := accessIdentityGateCheck(namedAccessConfig([]string{" ", ""}, true))
	if !strings.HasPrefix(blank.Detail, "WARNING:") {
		t.Errorf("blank entries must not count as an allowlist: %q", blank.Detail)
	}
}

// TestAccessIdentityGateCheckFailsOnInvalidState covers the defensive branch: a
// caller that skipped config validation must not get a passing check.
func TestAccessIdentityGateCheckFailsOnInvalidState(t *testing.T) {
	t.Parallel()
	c, ok := accessIdentityGateCheck(namedAccessConfig(nil, false))
	if !ok {
		t.Fatal("named mode must produce an identity-gate check")
	}
	if c.OK {
		t.Errorf("empty allowlist without the opt-out must fail: %+v", c)
	}
	if !strings.Contains(c.Detail, "invalid") {
		t.Errorf("detail should say the configuration is invalid: %q", c.Detail)
	}
}

// TestAccessIdentityGateCheckSkippedInQuickMode keeps quick mode untouched: it has
// no edge identity, so pairing is its gate and an allowlist means nothing there.
func TestAccessIdentityGateCheckSkippedInQuickMode(t *testing.T) {
	t.Parallel()
	cfg := config.Config{
		Cloudflare: config.Cloudflare{Mode: config.ModeQuick, QuickEnabled: true},
		Access:     config.Access{Enabled: false},
	}
	if _, ok := accessIdentityGateCheck(cfg); ok {
		t.Error("quick mode must not report an identity-gate check")
	}
	// Even with an Access block and no allowlist, quick mode reports nothing.
	cfg.Access = config.Access{Enabled: true, TeamDomain: "team.cloudflareaccess.com", Audience: "aud"}
	if _, ok := accessIdentityGateCheck(cfg); ok {
		t.Error("quick mode must not report an identity-gate check even with Access configured")
	}
}
