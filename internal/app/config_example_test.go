package app

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/matheus3301/herdr-phone/internal/config"
)

// TestConfigExampleValidAndMatchesDefaults verifies config.example.toml is a
// valid configuration and that every value it leaves at the documented default
// actually equals the built-in default, so the example cannot drift.
func TestConfigExampleValidAndMatchesDefaults(t *testing.T) {
	t.Parallel()
	data, err := os.ReadFile(filepath.Join(repoRoot(t), "config.example.toml"))
	if err != nil {
		t.Fatalf("read config.example.toml: %v", err)
	}
	got, err := config.LoadData(data, func(k string) string {
		if k == "HOME" {
			return "/home/example"
		}
		return ""
	})
	if err != nil {
		t.Fatalf("config.example.toml must be valid: %v", err)
	}
	def := config.Default()

	// Server, herdr, and ui blocks in the example are shown at defaults.
	if got.Server.Host != def.Server.Host ||
		got.Server.Port != def.Server.Port ||
		got.Server.SessionTTL != def.Server.SessionTTL ||
		got.Server.IdleLock != def.Server.IdleLock {
		t.Errorf("example server block drifted from defaults:\n got %+v\n def %+v", got.Server, def.Server)
	}
	if got.Herdr != def.Herdr {
		t.Errorf("example herdr block drifted:\n got %+v\n def %+v", got.Herdr, def.Herdr)
	}
	if got.UI != def.UI {
		t.Errorf("example ui block drifted:\n got %+v\n def %+v", got.UI, def.UI)
	}
	// Cloudflare mode/binary/grace and quick flag are at defaults; credentials differ.
	if got.Cloudflare.Mode != def.Cloudflare.Mode ||
		got.Cloudflare.Binary != def.Cloudflare.Binary ||
		got.Cloudflare.GracePeriod != def.Cloudflare.GracePeriod ||
		got.Cloudflare.QuickEnabled != def.Cloudflare.QuickEnabled {
		t.Errorf("example cloudflare defaults drifted:\n got %+v\n def %+v", got.Cloudflare, def.Cloudflare)
	}
	// Access enabled and jwks_ttl at defaults; team_domain/audience are set.
	if got.Access.Enabled != def.Access.Enabled || got.Access.JWKSTTL != def.Access.JWKSTTL {
		t.Errorf("example access defaults drifted:\n got %+v\n def %+v", got.Access, def.Access)
	}
	if got.Access.TeamDomain == "" || got.Access.Audience == "" {
		t.Error("example must show a team_domain and audience for named mode")
	}
	// The example demonstrates the token-command credential strategy.
	if len(got.Cloudflare.TokenCommand) == 0 {
		t.Error("example should demonstrate a credential strategy (token_command)")
	}
}
