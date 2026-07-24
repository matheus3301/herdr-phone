package tunnel

import (
	"strings"
	"testing"
	"time"
)

func TestBuildArgsQuick(t *testing.T) {
	t.Parallel()
	c := Config{Mode: ModeQuick, QuickEnabled: true, LoopbackPort: 8787}
	if err := c.Validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}
	args, err := buildArgs(c, "")
	if err != nil {
		t.Fatalf("buildArgs: %v", err)
	}
	joined := strings.Join(args, " ")
	for _, want := range []string{"tunnel", "--no-autoupdate", "--loglevel info", "--output json", "--metrics 127.0.0.1:0", "--url http://127.0.0.1:8787"} {
		if !strings.Contains(joined, want) {
			t.Errorf("quick args missing %q in %q", want, joined)
		}
	}
	if strings.Contains(joined, "run") {
		t.Errorf("quick args should not contain run: %q", joined)
	}
}

func TestBuildArgsNamedConfig(t *testing.T) {
	t.Parallel()
	c := Config{Mode: ModeNamed, PublicURL: "https://h.example.com", ConfigFile: "/etc/cf/config.yml", LoopbackPort: 8787}
	if err := c.Validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}
	args, _ := buildArgs(c, "")
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "--config /etc/cf/config.yml run") {
		t.Errorf("named config args wrong: %q", joined)
	}
}

func TestBuildArgsNamedCredentials(t *testing.T) {
	t.Parallel()
	c := Config{Mode: ModeNamed, PublicURL: "https://h.example.com", CredentialsFile: "/c/creds.json", Tunnel: "my-tunnel", LoopbackPort: 8787}
	if err := c.Validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}
	args, _ := buildArgs(c, "")
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "run --credentials-file /c/creds.json my-tunnel") {
		t.Errorf("named credentials args wrong: %q", joined)
	}
}

func TestBuildArgsTokenFileUsesPathNotSecret(t *testing.T) {
	t.Parallel()
	c := Config{Mode: ModeNamed, PublicURL: "https://h.example.com", TokenFile: "/run/tok", LoopbackPort: 8787}
	if err := c.Validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}
	args, _ := buildArgs(c, "/tmp/resolved-token")
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "run --token-file /tmp/resolved-token") {
		t.Errorf("token-file args wrong: %q", joined)
	}
	// The --token (value) form must never be produced.
	if strings.Contains(joined, "--token ") {
		t.Errorf("argv must never carry a bare --token value: %q", joined)
	}
}

func TestBuildArgsTokenStrategyRequiresPath(t *testing.T) {
	t.Parallel()
	c := Config{Mode: ModeNamed, PublicURL: "https://h.example.com", TokenCommand: []string{"echo", "x"}, LoopbackPort: 8787}
	if err := c.Validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}
	if _, err := buildArgs(c, ""); err == nil {
		t.Fatal("expected error when token path is empty for token strategy")
	}
}

func TestValidate(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		cfg     Config
		wantErr bool
	}{
		{"quick disabled", Config{Mode: ModeQuick, LoopbackPort: 8787}, true},
		{"quick enabled", Config{Mode: ModeQuick, QuickEnabled: true, LoopbackPort: 8787}, false},
		{"bad port", Config{Mode: ModeQuick, QuickEnabled: true, LoopbackPort: 0}, true},
		{"port too high", Config{Mode: ModeQuick, QuickEnabled: true, LoopbackPort: 70000}, true},
		{"named no strategy", Config{Mode: ModeNamed, PublicURL: "https://h", LoopbackPort: 8787}, true},
		{"named no url", Config{Mode: ModeNamed, ConfigFile: "/x", LoopbackPort: 8787}, true},
		{"named two strategies", Config{Mode: ModeNamed, PublicURL: "https://h", ConfigFile: "/x", TokenFile: "/y", LoopbackPort: 8787}, true},
		{"named config ok", Config{Mode: ModeNamed, PublicURL: "https://h", ConfigFile: "/x", LoopbackPort: 8787}, false},
		{"credentials missing tunnel", Config{Mode: ModeNamed, PublicURL: "https://h", CredentialsFile: "/c", LoopbackPort: 8787}, true},
		{"unknown mode", Config{Mode: Mode("weird"), LoopbackPort: 8787}, true},
		{"empty mode", Config{LoopbackPort: 8787}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := tc.cfg
			err := cfg.Validate()
			if tc.wantErr != (err != nil) {
				t.Fatalf("Validate() err=%v wantErr=%v", err, tc.wantErr)
			}
		})
	}
}

func TestValidateAppliesDefaults(t *testing.T) {
	t.Parallel()
	c := Config{Mode: ModeQuick, QuickEnabled: true, LoopbackPort: 8787}
	if err := c.Validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}
	if c.Binary != defaultBinary {
		t.Errorf("binary default = %q", c.Binary)
	}
	if c.MetricsAddr != defaultMetricsAddr {
		t.Errorf("metrics default = %q", c.MetricsAddr)
	}
	if c.GracePeriod != 15*time.Second {
		t.Errorf("grace default = %v", c.GracePeriod)
	}
}
