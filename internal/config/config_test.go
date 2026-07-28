package config

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// envMap builds a Getenv function from a map for deterministic tests.
func envMap(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

// validNamed is a complete, valid named-mode configuration used as a base that
// individual tests mutate to exercise a single failure. It carries a non-empty
// allowed_identities because named mode is Access-only: the allowlist is the last
// origin-side identity filter, so a config without one (and without the explicit
// allow_any_identity opt-out) is invalid.
const validNamed = `
[server]
host = "127.0.0.1"
port = 8787

[cloudflare]
mode = "named"
public_url = "https://herdr.example.com"
token_command = ["print-token"]

[auth.access]
enabled = true
team_domain = "example.cloudflareaccess.com"
audience = "aud-123"
allowed_identities = ["op@example.com"]
`

const validQuick = `
[cloudflare]
mode = "quick"
quick_enabled = true
`

func mustLoad(t *testing.T, body string) Config {
	t.Helper()
	cfg, err := LoadData([]byte(body), envMap(map[string]string{"HOME": "/home/tester"}))
	if err != nil {
		t.Fatalf("expected valid config, got error: %v", err)
	}
	return cfg
}

func mustReject(t *testing.T, body, wantSubstr string) {
	t.Helper()
	_, err := LoadData([]byte(body), envMap(map[string]string{"HOME": "/home/tester"}))
	if err == nil {
		t.Fatalf("expected error containing %q, got nil", wantSubstr)
	}
	if !strings.Contains(err.Error(), wantSubstr) {
		t.Fatalf("error %q does not contain %q", err.Error(), wantSubstr)
	}
}

func TestDefaults(t *testing.T) {
	t.Parallel()
	d := Default()
	if d.Server.Host != LoopbackHost || d.Server.Port != DefaultPort {
		t.Errorf("server defaults: %+v", d.Server)
	}
	if d.Server.SessionTTL != DefaultSessionTTL || d.Server.IdleLock != DefaultIdleLock {
		t.Errorf("server ttl defaults: %+v", d.Server)
	}
	if d.Cloudflare.Mode != ModeNamed || d.Cloudflare.Binary != DefaultCloudflareBinary || d.Cloudflare.GracePeriod != DefaultGracePeriod {
		t.Errorf("cloudflare defaults: %+v", d.Cloudflare)
	}
	if !d.Access.Enabled || d.Access.JWKSTTL != DefaultJWKSTTL {
		t.Errorf("access defaults: %+v", d.Access)
	}
	// No implicit blanket allow: the opt-out must be declared, never defaulted.
	if d.Access.AllowAnyIdentity || d.Access.HasIdentityAllowlist() {
		t.Errorf("access identity gate defaults: %+v", d.Access)
	}
	if d.Herdr.PollHot != DefaultPollHot || d.Herdr.PollCold != DefaultPollCold {
		t.Errorf("herdr defaults: %+v", d.Herdr)
	}
	if d.UI.Theme != ThemeSystem || d.UI.TerminalFontSize != DefaultTerminalFontSize {
		t.Errorf("ui defaults: %+v", d.UI)
	}
}

func TestPathPrecedence(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		env  map[string]string
		want string
	}{
		{"plugin config dir", map[string]string{"HERDR_PLUGIN_CONFIG_DIR": "/plugcfg", "XDG_CONFIG_HOME": "/xdg", "HOME": "/home/u"}, "/plugcfg/config.toml"},
		{"xdg", map[string]string{"XDG_CONFIG_HOME": "/xdg", "HOME": "/home/u"}, "/xdg/herdr-phone/config.toml"},
		{"home", map[string]string{"HOME": "/home/u"}, "/home/u/.config/herdr-phone/config.toml"},
		{"none", map[string]string{}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Path(envMap(tc.env)); got != tc.want {
				t.Errorf("Path = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestValidNamedAndQuick(t *testing.T) {
	t.Parallel()
	named := mustLoad(t, validNamed)
	if named.Cloudflare.Mode != ModeNamed || named.Cloudflare.PublicURL != "https://herdr.example.com" {
		t.Errorf("named config not loaded: %+v", named.Cloudflare)
	}
	quick := mustLoad(t, validQuick)
	if quick.Cloudflare.Mode != ModeQuick || !quick.Cloudflare.QuickEnabled {
		t.Errorf("quick config not loaded: %+v", quick.Cloudflare)
	}
}

func TestUnknownKeyRejected(t *testing.T) {
	t.Parallel()
	mustReject(t, validQuick+"\n[server]\nbogus_key = 1\n", "unknown configuration key")
}

func TestUnknownTopLevelKeyRejected(t *testing.T) {
	t.Parallel()
	mustReject(t, validQuick+"\nsurprise = true\n", "unknown configuration key")
}

func TestQuickRequiresEnabled(t *testing.T) {
	t.Parallel()
	mustReject(t, "[cloudflare]\nmode = \"quick\"\nquick_enabled = false\n", "quick_enabled")
}

func TestInvalidMode(t *testing.T) {
	t.Parallel()
	mustReject(t, "[cloudflare]\nmode = \"public\"\n", "cloudflare.mode must be")
}

func TestNamedRequiresAccessEnabled(t *testing.T) {
	t.Parallel()
	body := strings.Replace(validNamed, "enabled = true", "enabled = false", 1)
	mustReject(t, body, "auth.access.enabled = true")
}

func TestNamedRequiresHTTPSPublicURL(t *testing.T) {
	t.Parallel()
	body := strings.Replace(validNamed, "https://herdr.example.com", "http://herdr.example.com", 1)
	mustReject(t, body, "must use https")
}

func TestNamedRequiresPublicURL(t *testing.T) {
	t.Parallel()
	body := `
[cloudflare]
mode = "named"
token_command = ["print-token"]
[auth.access]
enabled = true
team_domain = "example.cloudflareaccess.com"
audience = "aud"
`
	mustReject(t, body, "requires cloudflare.public_url")
}

func TestNamedRequiresExactlyOneCredentialStrategy(t *testing.T) {
	t.Parallel()
	none := `
[cloudflare]
mode = "named"
public_url = "https://h.example.com"
[auth.access]
enabled = true
team_domain = "example.cloudflareaccess.com"
audience = "aud"
`
	mustReject(t, none, "exactly one cloudflared credential strategy")

	// Two strategies configured at once.
	two := none + "token_file = \"/tmp/tok\"\ntoken_command = [\"print-token\"]\n"
	// token_file/token_command belong under [cloudflare]; rebuild cleanly.
	two = `
[cloudflare]
mode = "named"
public_url = "https://h.example.com"
token_file = "/tmp/tok"
token_command = ["print-token"]
[auth.access]
enabled = true
team_domain = "example.cloudflareaccess.com"
audience = "aud"
`
	mustReject(t, two, "2 are configured")
}

func TestNamedAccessRequiresTeamDomainAndAudience(t *testing.T) {
	t.Parallel()
	noTeam := strings.Replace(validNamed, `team_domain = "example.cloudflareaccess.com"`, `team_domain = ""`, 1)
	mustReject(t, noTeam, "team_domain")
	noAud := strings.Replace(validNamed, `audience = "aud-123"`, `audience = ""`, 1)
	mustReject(t, noAud, "audience")
}

// ---- named-mode identity gate ---------------------------------------------
//
// Named mode is Access-only since v0.3.0: pairing is no longer a second factor
// there, so auth.access.allowed_identities is the last identity filter the origin
// applies. It must be non-empty unless the operator declares allow_any_identity.

// namedWithoutAllowlist is validNamed with the allowlist line removed.
func namedWithoutAllowlist() string {
	return strings.Replace(validNamed, "allowed_identities = [\"op@example.com\"]\n", "", 1)
}

func TestNamedRequiresIdentityAllowlist(t *testing.T) {
	t.Parallel()
	// Absent entirely.
	mustReject(t, namedWithoutAllowlist(), "requires a non-empty auth.access.allowed_identities")
	// Explicitly empty.
	mustReject(t, namedWithoutAllowlist()+"allowed_identities = []\n", "requires a non-empty auth.access.allowed_identities")
	// The opt-out must be explicitly true, not merely present.
	mustReject(t, namedWithoutAllowlist()+"allow_any_identity = false\n", "requires a non-empty auth.access.allowed_identities")
}

func TestNamedIdentityAllowlistErrorIsActionable(t *testing.T) {
	t.Parallel()
	_, err := LoadData([]byte(namedWithoutAllowlist()), envMap(map[string]string{"HOME": "/home/tester"}))
	if err == nil {
		t.Fatal("expected an error for a named config with no identity allowlist")
	}
	// The operator must be told both remedies by name, and see a usable example.
	for _, want := range []string{
		"auth.access.allowed_identities",
		`allowed_identities = ["you@example.com"]`,
		"auth.access.allow_any_identity = true",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error must mention %q; got: %v", want, err)
		}
	}
}

func TestNamedAllowAnyIdentityOptOut(t *testing.T) {
	t.Parallel()
	cfg := mustLoad(t, namedWithoutAllowlist()+"allow_any_identity = true\n")
	if !cfg.Access.AllowAnyIdentity {
		t.Error("allow_any_identity = true must be parsed")
	}
	if cfg.Access.HasIdentityAllowlist() {
		t.Error("no allowlist was configured")
	}
	// Explicitly empty list plus the opt-out is equally valid.
	cfg2 := mustLoad(t, namedWithoutAllowlist()+"allowed_identities = []\nallow_any_identity = true\n")
	if !cfg2.Access.AllowAnyIdentity || cfg2.Access.HasIdentityAllowlist() {
		t.Errorf("access = %+v", cfg2.Access)
	}
}

func TestNamedIdentityAllowlistAccepted(t *testing.T) {
	t.Parallel()
	cfg := mustLoad(t, validNamed)
	if !cfg.Access.HasIdentityAllowlist() {
		t.Fatal("a non-empty allowlist must be recognized")
	}
	if len(cfg.Access.AllowedIdentities) != 1 || cfg.Access.AllowedIdentities[0] != "op@example.com" {
		t.Errorf("allowed_identities = %v", cfg.Access.AllowedIdentities)
	}
	if cfg.Access.AllowAnyIdentity {
		t.Error("allow_any_identity must default to false")
	}

	// The opt-out waives only the requirement, never the enforcement: a configured
	// allowlist is retained (and still matched by the verifier) alongside it.
	both := strings.Replace(validNamed, "allowed_identities = [\"op@example.com\"]",
		"allowed_identities = [\"op@example.com\"]\nallow_any_identity = true", 1)
	cfgBoth := mustLoad(t, both)
	if !cfgBoth.Access.HasIdentityAllowlist() || !cfgBoth.Access.AllowAnyIdentity {
		t.Errorf("access = %+v", cfgBoth.Access)
	}
}

// TestBlankIdentityIsNotAnAllowlist keeps a whitespace-only entry from passing as
// an allowlist: the verifier drops blank entries, so accepting one here would
// reinstate exactly the wide-open state this validation exists to prevent.
func TestBlankIdentityIsNotAnAllowlist(t *testing.T) {
	t.Parallel()
	mustReject(t, namedWithoutAllowlist()+"allowed_identities = [\"   \"]\n", "must not be empty")
	if (Access{AllowedIdentities: []string{"", "  "}}).HasIdentityAllowlist() {
		t.Error("blank entries must not count as an allowlist")
	}
}

// TestQuickModeIgnoresIdentityAllowlist pins the quick-mode contract: a quick
// tunnel has no edge identity, so pairing is its gate and the allowlist
// requirement must not apply.
func TestQuickModeIgnoresIdentityAllowlist(t *testing.T) {
	t.Parallel()
	// No [auth.access] block at all.
	if cfg := mustLoad(t, validQuick); cfg.Access.HasIdentityAllowlist() || cfg.Access.AllowAnyIdentity {
		t.Errorf("quick access = %+v", cfg.Access)
	}
	// An Access block with an explicitly empty allowlist and no opt-out is still
	// valid in quick mode.
	withAccess := validQuick + `
[auth.access]
enabled = true
team_domain = "example.cloudflareaccess.com"
audience = "aud-123"
allowed_identities = []
`
	if cfg := mustLoad(t, withAccess); cfg.Cloudflare.Mode != ModeQuick {
		t.Errorf("mode = %q", cfg.Cloudflare.Mode)
	}
}

func TestTeamDomainFormat(t *testing.T) {
	t.Parallel()
	bad := strings.Replace(validNamed, `team_domain = "example.cloudflareaccess.com"`, `team_domain = "https://example.com/x"`, 1)
	mustReject(t, bad, "bare hostname")
}

func TestHostMustBeLoopback(t *testing.T) {
	t.Parallel()
	body := strings.Replace(validQuick, "[cloudflare]", "[server]\nhost = \"0.0.0.0\"\n\n[cloudflare]", 1)
	mustReject(t, body, "server.host must be exactly")
}

func TestPortRange(t *testing.T) {
	t.Parallel()
	body := "[server]\nport = 70000\n" + validQuick
	mustReject(t, body, "server.port must be between")
}

func TestPollHotFloor(t *testing.T) {
	t.Parallel()
	body := validQuick + "\n[herdr]\npoll_hot = \"100ms\"\n"
	mustReject(t, body, "herdr.poll_hot must be at least")
}

func TestPollColdNotShorterThanHot(t *testing.T) {
	t.Parallel()
	body := validQuick + "\n[herdr]\npoll_hot = \"5s\"\npoll_cold = \"2s\"\n"
	mustReject(t, body, "must not be shorter")
}

func TestIdleLockNotExceedSessionTTL(t *testing.T) {
	t.Parallel()
	body := "[server]\nsession_ttl = \"10m\"\nidle_lock = \"30m\"\n" + validQuick
	mustReject(t, body, "must not exceed")
}

func TestNonPositiveDurationRejected(t *testing.T) {
	t.Parallel()
	body := "[server]\nsession_ttl = \"0s\"\n" + validQuick
	mustReject(t, body, "must be positive")
}

func TestInvalidDurationString(t *testing.T) {
	t.Parallel()
	body := "[server]\nsession_ttl = \"12 hours\"\n" + validQuick
	mustReject(t, body, "invalid duration")
}

func TestThemeValidation(t *testing.T) {
	t.Parallel()
	body := validQuick + "\n[ui]\ntheme = \"neon\"\n"
	mustReject(t, body, "ui.theme must be one of")
}

func TestTerminalFontSizeRange(t *testing.T) {
	t.Parallel()
	body := validQuick + "\n[ui]\nterminal_font_size = 200\n"
	mustReject(t, body, "ui.terminal_font_size must be between")
}

func TestTokenCommandEmptyEntryRejected(t *testing.T) {
	t.Parallel()
	body := `
[cloudflare]
mode = "named"
public_url = "https://h.example.com"
token_command = ["security", ""]
[auth.access]
enabled = true
team_domain = "example.cloudflareaccess.com"
audience = "aud"
`
	mustReject(t, body, "token_command[1]")
}

func TestPathExpansion(t *testing.T) {
	t.Parallel()
	body := `
[server]
allowed_workspace_roots = ["~/src", "$WORK/repos"]
` + validQuick
	cfg, err := LoadData([]byte(body), envMap(map[string]string{"HOME": "/home/tester", "WORK": "/data"}))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	want := []string{filepath.Clean("/home/tester/src"), filepath.Clean("/data/repos")}
	if got := cfg.Server.AllowedWorkspaceRoots; got[0] != want[0] || got[1] != want[1] {
		t.Errorf("expanded roots = %v, want %v", got, want)
	}
}

func TestPathExpansionUnsetVarErrors(t *testing.T) {
	t.Parallel()
	body := "[server]\nallowed_workspace_roots = [\"$UNSET_ROOT/x\"]\n" + validQuick
	_, err := LoadData([]byte(body), envMap(map[string]string{"HOME": "/home/tester"}))
	if err == nil || !strings.Contains(err.Error(), "UNSET_ROOT") {
		t.Fatalf("expected unset-variable error, got %v", err)
	}
}

func TestQuickModeIgnoresAccess(t *testing.T) {
	t.Parallel()
	// Quick mode does not require Access config even though it is absent.
	cfg := mustLoad(t, validQuick)
	if cfg.Cloudflare.Mode != ModeQuick {
		t.Fatalf("mode = %q", cfg.Cloudflare.Mode)
	}
}

func TestGracePeriodBounded(t *testing.T) {
	t.Parallel()
	body := validQuick + "\ngrace_period = \"10m\"\n"
	mustReject(t, body, "cloudflare.grace_period must be at most")
}

func TestDurationsParsedIntoConfig(t *testing.T) {
	t.Parallel()
	cfg := mustLoad(t, validQuick+"\n[server]\nsession_ttl = \"6h\"\n")
	if cfg.Server.SessionTTL != 6*time.Hour {
		t.Errorf("session_ttl = %s", cfg.Server.SessionTTL)
	}
}
