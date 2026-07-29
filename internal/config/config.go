// Package config loads and strictly validates herdr-phone configuration.
//
// Configuration is TOML. Unknown keys are rejected, defaults are applied before
// decoding, and every value is validated with an actionable, field-specific
// error. Paths are expanded (leading "~" and explicit environment variables,
// erroring on an unset variable) but no shell is ever executed. The package
// never logs or otherwise exposes secret material: token commands are argv
// arrays resolved separately (see secret.go) and their output never touches a
// config value or an error string.
package config

import (
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/BurntSushi/toml"

	"github.com/matheus3301/herdr-phone/internal/interpret"
)

// Cloudflare front-door modes.
const (
	ModeNamed = "named"
	ModeQuick = "quick"
)

// UI themes.
const (
	ThemeSystem = "system"
	ThemeLight  = "light"
	ThemeDark   = "dark"
)

// LoopbackHost is the only bind host permitted for the origin server. The relay
// is exposed exclusively through a Cloudflare tunnel; it must never bind a
// routable interface.
const LoopbackHost = "127.0.0.1"

// Documented default values. Keep these synchronized with config.example.toml
// (enforced by tests in internal/app).
const (
	DefaultPort             = 8787
	DefaultSessionTTL       = 12 * time.Hour
	DefaultIdleLock         = 30 * time.Minute
	DefaultCloudflareBinary = "cloudflared"
	DefaultGracePeriod      = 15 * time.Second
	DefaultJWKSTTL          = time.Hour
	DefaultPollHot          = 1500 * time.Millisecond
	DefaultPollCold         = 12 * time.Second
	DefaultTerminalFontSize = 13
	// DefaultMaxInterpretedTurns bounds the experimental transcript. It matches
	// interpret.DefaultLimits().MaxTurns.
	DefaultMaxInterpretedTurns = 60
)

// Validation bounds.
const (
	minPort            = 1
	maxPort            = 65535
	minPollHot         = 250 * time.Millisecond
	maxDuration        = 30 * 24 * time.Hour // 30 days: a generous ceiling for TTLs.
	maxGracePeriod     = 5 * time.Minute
	maxPollInterval    = 10 * time.Minute
	minTerminalFont    = 8
	maxTerminalFont    = 72
	maxInterpretedTurn = 500 // a chat view nobody can scroll is not a feature
	maxIdentityRuneLen = 320 // RFC 5321 practical maximum for an email address.
)

// Config is the fully resolved, validated configuration.
type Config struct {
	Server       Server
	Cloudflare   Cloudflare
	Access       Access
	Herdr        Herdr
	UI           UI
	Experimental Experimental
	// SourcePath is the file the config was loaded from, or "" when built-in
	// defaults were used.
	SourcePath string
}

// Server holds the loopback origin server configuration.
type Server struct {
	Host                  string
	Port                  int
	SessionTTL            time.Duration
	IdleLock              time.Duration
	AllowedWorkspaceRoots []string
}

// Cloudflare holds cloudflared front-door configuration. Exactly one credential
// strategy is used in named mode: locally-managed config/credentials, a token
// file, or a token command.
type Cloudflare struct {
	Mode            string
	Binary          string
	PublicURL       string
	ConfigFile      string
	Tunnel          string
	CredentialsFile string
	TokenFile       string
	TokenCommand    []string
	QuickEnabled    bool
	GracePeriod     time.Duration
}

// Access holds Cloudflare Access edge-identity configuration for named mode.
type Access struct {
	Enabled           bool
	TeamDomain        string
	Audience          string
	AllowedIdentities []string
	// AllowAnyIdentity waives the requirement that named mode carry a non-empty
	// AllowedIdentities. Since named mode became Access-only, the allowlist is the
	// last origin-side identity filter, so an empty one means every identity the
	// Cloudflare Access policy admits reaches a shell-equivalent surface. That is a
	// legitimate configuration only when the Access policy itself is the intended
	// boundary, so it must be declared deliberately.
	//
	// It waives only the requirement, never the enforcement: a non-empty
	// AllowedIdentities is still matched exactly by the verifier even when this is
	// true, so the stricter of the two always wins.
	AllowAnyIdentity bool
	JWKSTTL          time.Duration
}

// Herdr holds Herdr connection and polling configuration.
type Herdr struct {
	SocketPath string
	Binary     string
	PollHot    time.Duration
	PollCold   time.Duration
}

// UI holds presentation defaults surfaced to the embedded frontend.
type UI struct {
	Theme            string
	TerminalFontSize int
}

// Experimental holds opt-in behaviour that is off by default because it is not
// authoritative. Nothing in here may change how the relay behaves unless the
// operator turned it on explicitly (SPEC §12.2).
type Experimental struct {
	// AgentOutputParsing enables heuristic interpretation of agent terminal text.
	// While false, no parser code runs and the run contract is byte-identical to
	// the non-experimental one.
	AgentOutputParsing bool
	// AgentOutputParsers is the set of agent kinds whose grammar may be parsed.
	// Every entry must name a parser this build implements, so a typo fails at
	// start rather than silently parsing nothing.
	AgentOutputParsers []string
	// MaxInterpretedTurns bounds how many turns one read may publish.
	MaxInterpretedTurns int
}

// Default returns the built-in default configuration. Named mode with empty
// credentials is deliberately not a runnable configuration: a real deployment
// must supply a public URL and a credential strategy (see config.example.toml).
func Default() Config {
	return Config{
		Server: Server{
			Host:                  LoopbackHost,
			Port:                  DefaultPort,
			SessionTTL:            DefaultSessionTTL,
			IdleLock:              DefaultIdleLock,
			AllowedWorkspaceRoots: []string{"~"},
		},
		Cloudflare: Cloudflare{
			Mode:        ModeNamed,
			Binary:      DefaultCloudflareBinary,
			GracePeriod: DefaultGracePeriod,
		},
		Access: Access{
			Enabled: true,
			// No implicit blanket allow: named mode must either name the identities
			// that may reach the relay or declare allow_any_identity deliberately.
			AllowAnyIdentity: false,
			JWKSTTL:          DefaultJWKSTTL,
		},
		Herdr: Herdr{
			PollHot:  DefaultPollHot,
			PollCold: DefaultPollCold,
		},
		UI: UI{
			Theme:            ThemeSystem,
			TerminalFontSize: DefaultTerminalFontSize,
		},
		Experimental: Experimental{
			// Off by default, deliberately. Enabling this publishes guesses about a
			// third-party TUI's layout; that is a decision an operator makes, never a
			// default they inherit.
			AgentOutputParsing:  false,
			AgentOutputParsers:  defaultAgentOutputParsers(),
			MaxInterpretedTurns: DefaultMaxInterpretedTurns,
		},
	}
}

// defaultAgentOutputParsers lists every parser this build implements. It is only
// consulted when AgentOutputParsing is true.
func defaultAgentOutputParsers() []string {
	kinds := interpret.ParserKinds()
	out := make([]string, 0, len(kinds))
	for _, k := range kinds {
		out = append(out, string(k))
	}
	return out
}

// Path resolves the configuration file path following the documented precedence.
// It returns "" when no location can be determined.
func Path(env func(string) string) string {
	if env == nil {
		env = os.Getenv
	}
	candidates := candidatePaths(env)
	// Choose the first candidate that actually exists, in precedence order, so a
	// higher-precedence env var pointing at a directory with no config.toml does
	// not shadow a real config in a lower-precedence location.
	for _, c := range candidates {
		if fileExists(c) {
			return c
		}
	}
	// None exist: return the highest-precedence candidate so callers report a
	// sensible location (Load then treats the missing file as defaults).
	if len(candidates) > 0 {
		return candidates[0]
	}
	return ""
}

// candidatePaths returns the config.toml paths to try, in precedence order, for
// each location whose environment variable is set.
func candidatePaths(env func(string) string) []string {
	var paths []string
	if dir := env("HERDR_PLUGIN_CONFIG_DIR"); dir != "" {
		paths = append(paths, filepath.Join(dir, "config.toml"))
	}
	if dir := env("XDG_CONFIG_HOME"); dir != "" {
		paths = append(paths, filepath.Join(dir, "herdr-phone", "config.toml"))
	}
	if home := env("HOME"); home != "" {
		paths = append(paths, filepath.Join(home, ".config", "herdr-phone", "config.toml"))
	}
	return paths
}

// fileExists reports whether path is a regular file that can be stat'd.
func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}

// Load resolves the config path from env, loads the file when present, applies
// defaults for anything unset, expands paths, and validates the result. Unlike a
// read-only dashboard, herdr-phone cannot run on defaults alone, so a missing
// file still yields the (invalid) defaults and the resulting validation error
// tells the operator exactly what to configure.
func Load(env func(string) string) (Config, error) {
	if env == nil {
		env = os.Getenv
	}
	cfg := Default()
	path := Path(env)
	if path != "" {
		data, err := os.ReadFile(path)
		switch {
		case err == nil:
			if err := decodeInto(&cfg, data); err != nil {
				return Config{}, fmt.Errorf("config %s: %w", path, err)
			}
			cfg.SourcePath = path
		case errors.Is(err, fs.ErrNotExist):
			// Fall through to expansion/validation on the defaults.
		default:
			return Config{}, fmt.Errorf("read config %s: %w", path, err)
		}
	}
	if err := cfg.expand(env); err != nil {
		return Config{}, err
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// LoadData parses config bytes over the defaults, expands paths, and validates.
// It is used by tests and by callers that already hold config bytes.
func LoadData(data []byte, env func(string) string) (Config, error) {
	if env == nil {
		env = os.Getenv
	}
	cfg := Default()
	if err := decodeInto(&cfg, data); err != nil {
		return Config{}, err
	}
	if err := cfg.expand(env); err != nil {
		return Config{}, err
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

type rawConfig struct {
	Server       *rawServer       `toml:"server"`
	Cloudflare   *rawCloudflare   `toml:"cloudflare"`
	Auth         *rawAuth         `toml:"auth"`
	Herdr        *rawHerdr        `toml:"herdr"`
	UI           *rawUI           `toml:"ui"`
	Experimental *rawExperimental `toml:"experimental"`
}

type rawServer struct {
	Host                  *string   `toml:"host"`
	Port                  *int      `toml:"port"`
	SessionTTL            *string   `toml:"session_ttl"`
	IdleLock              *string   `toml:"idle_lock"`
	AllowedWorkspaceRoots *[]string `toml:"allowed_workspace_roots"`
}

type rawCloudflare struct {
	Mode            *string  `toml:"mode"`
	Binary          *string  `toml:"binary"`
	PublicURL       *string  `toml:"public_url"`
	ConfigFile      *string  `toml:"config_file"`
	Tunnel          *string  `toml:"tunnel"`
	CredentialsFile *string  `toml:"credentials_file"`
	TokenFile       *string  `toml:"token_file"`
	TokenCommand    []string `toml:"token_command"`
	QuickEnabled    *bool    `toml:"quick_enabled"`
	GracePeriod     *string  `toml:"grace_period"`
}

type rawAuth struct {
	Access *rawAccess `toml:"access"`
}

type rawAccess struct {
	Enabled           *bool    `toml:"enabled"`
	TeamDomain        *string  `toml:"team_domain"`
	Audience          *string  `toml:"audience"`
	AllowedIdentities []string `toml:"allowed_identities"`
	AllowAnyIdentity  *bool    `toml:"allow_any_identity"`
	JWKSTTL           *string  `toml:"jwks_ttl"`
}

type rawHerdr struct {
	SocketPath *string `toml:"socket_path"`
	Binary     *string `toml:"binary"`
	PollHot    *string `toml:"poll_hot"`
	PollCold   *string `toml:"poll_cold"`
}

type rawUI struct {
	Theme            *string `toml:"theme"`
	TerminalFontSize *int    `toml:"terminal_font_size"`
}

type rawExperimental struct {
	AgentOutputParsing *bool     `toml:"agent_output_parsing"`
	AgentOutputParsers *[]string `toml:"agent_output_parsers"`
	// MaxInterpretedTurns is a pointer so an explicit 0 is a validation error
	// rather than silently taking the default.
	MaxInterpretedTurns *int `toml:"max_interpreted_turns"`
}

func decodeInto(cfg *Config, data []byte) error {
	var raw rawConfig
	md, err := toml.Decode(string(data), &raw)
	if err != nil {
		return fmt.Errorf("parse TOML: %w", err)
	}
	if undecoded := md.Undecoded(); len(undecoded) > 0 {
		keys := make([]string, 0, len(undecoded))
		for _, k := range undecoded {
			keys = append(keys, k.String())
		}
		sort.Strings(keys)
		return fmt.Errorf("unknown configuration key(s): %s", strings.Join(keys, ", "))
	}
	return raw.applyTo(cfg)
}

func (r rawConfig) applyTo(cfg *Config) error {
	if s := r.Server; s != nil {
		if s.Host != nil {
			cfg.Server.Host = *s.Host
		}
		if s.Port != nil {
			cfg.Server.Port = *s.Port
		}
		if err := applyDuration(s.SessionTTL, &cfg.Server.SessionTTL, "server.session_ttl"); err != nil {
			return err
		}
		if err := applyDuration(s.IdleLock, &cfg.Server.IdleLock, "server.idle_lock"); err != nil {
			return err
		}
		if s.AllowedWorkspaceRoots != nil {
			cfg.Server.AllowedWorkspaceRoots = append([]string(nil), (*s.AllowedWorkspaceRoots)...)
		}
	}
	if c := r.Cloudflare; c != nil {
		applyString(c.Mode, &cfg.Cloudflare.Mode)
		applyString(c.Binary, &cfg.Cloudflare.Binary)
		applyString(c.PublicURL, &cfg.Cloudflare.PublicURL)
		applyString(c.ConfigFile, &cfg.Cloudflare.ConfigFile)
		applyString(c.Tunnel, &cfg.Cloudflare.Tunnel)
		applyString(c.CredentialsFile, &cfg.Cloudflare.CredentialsFile)
		applyString(c.TokenFile, &cfg.Cloudflare.TokenFile)
		if c.TokenCommand != nil {
			cfg.Cloudflare.TokenCommand = append([]string(nil), c.TokenCommand...)
		}
		if c.QuickEnabled != nil {
			cfg.Cloudflare.QuickEnabled = *c.QuickEnabled
		}
		if err := applyDuration(c.GracePeriod, &cfg.Cloudflare.GracePeriod, "cloudflare.grace_period"); err != nil {
			return err
		}
	}
	if r.Auth != nil && r.Auth.Access != nil {
		a := r.Auth.Access
		if a.Enabled != nil {
			cfg.Access.Enabled = *a.Enabled
		}
		applyString(a.TeamDomain, &cfg.Access.TeamDomain)
		applyString(a.Audience, &cfg.Access.Audience)
		if a.AllowedIdentities != nil {
			cfg.Access.AllowedIdentities = append([]string(nil), a.AllowedIdentities...)
		}
		if a.AllowAnyIdentity != nil {
			cfg.Access.AllowAnyIdentity = *a.AllowAnyIdentity
		}
		if err := applyDuration(a.JWKSTTL, &cfg.Access.JWKSTTL, "auth.access.jwks_ttl"); err != nil {
			return err
		}
	}
	if h := r.Herdr; h != nil {
		applyString(h.SocketPath, &cfg.Herdr.SocketPath)
		applyString(h.Binary, &cfg.Herdr.Binary)
		if err := applyDuration(h.PollHot, &cfg.Herdr.PollHot, "herdr.poll_hot"); err != nil {
			return err
		}
		if err := applyDuration(h.PollCold, &cfg.Herdr.PollCold, "herdr.poll_cold"); err != nil {
			return err
		}
	}
	if u := r.UI; u != nil {
		applyString(u.Theme, &cfg.UI.Theme)
		if u.TerminalFontSize != nil {
			cfg.UI.TerminalFontSize = *u.TerminalFontSize
		}
	}
	if e := r.Experimental; e != nil {
		if e.AgentOutputParsing != nil {
			cfg.Experimental.AgentOutputParsing = *e.AgentOutputParsing
		}
		if e.AgentOutputParsers != nil {
			// Replace rather than merge: an operator narrowing the list to one agent
			// must not silently keep the other.
			cfg.Experimental.AgentOutputParsers = append([]string(nil), (*e.AgentOutputParsers)...)
		}
		if e.MaxInterpretedTurns != nil {
			cfg.Experimental.MaxInterpretedTurns = *e.MaxInterpretedTurns
		}
	}
	return nil
}

func applyString(src *string, dst *string) {
	if src != nil {
		*dst = *src
	}
}

func applyDuration(src *string, dst *time.Duration, field string) error {
	if src == nil {
		return nil
	}
	d, err := time.ParseDuration(*src)
	if err != nil {
		return fmt.Errorf("%s: invalid duration %q: %v", field, *src, err)
	}
	*dst = d
	return nil
}

// expand rewrites path-like fields in place, expanding "~" and environment
// variables and erroring on an unset variable. It never runs a shell.
func (c *Config) expand(env func(string) string) error {
	for i, root := range c.Server.AllowedWorkspaceRoots {
		if strings.TrimSpace(root) == "" {
			return fmt.Errorf("server.allowed_workspace_roots[%d] must not be empty", i)
		}
		expanded, err := ExpandPath(root, env)
		if err != nil {
			return fmt.Errorf("server.allowed_workspace_roots[%d]: %w", i, err)
		}
		c.Server.AllowedWorkspaceRoots[i] = expanded
	}
	type field struct {
		name string
		ptr  *string
	}
	for _, f := range []field{
		{"cloudflare.config_file", &c.Cloudflare.ConfigFile},
		{"cloudflare.credentials_file", &c.Cloudflare.CredentialsFile},
		{"cloudflare.token_file", &c.Cloudflare.TokenFile},
		{"cloudflare.binary", &c.Cloudflare.Binary},
		{"herdr.socket_path", &c.Herdr.SocketPath},
		{"herdr.binary", &c.Herdr.Binary},
	} {
		expanded, err := expandIfPath(*f.ptr, env)
		if err != nil {
			return fmt.Errorf("%s: %w", f.name, err)
		}
		*f.ptr = expanded
	}
	return nil
}

// Validate checks every field with actionable, field-specific errors. It is
// purely structural: it performs no filesystem, network, or process access, so
// it is fully deterministic. Filesystem and runtime checks live in verify.go and
// in the daemon.
func (c Config) Validate() error {
	if err := c.Server.validate(); err != nil {
		return err
	}
	if err := c.Cloudflare.validate(c.Access); err != nil {
		return err
	}
	if err := c.Herdr.validate(); err != nil {
		return err
	}
	if err := c.Experimental.validate(); err != nil {
		return err
	}
	if err := c.UI.validate(); err != nil {
		return err
	}
	return nil
}

func (s Server) validate() error {
	if s.Host != LoopbackHost {
		return fmt.Errorf("server.host must be exactly %q (the origin is reachable only through the tunnel), got %q", LoopbackHost, s.Host)
	}
	if s.Port < minPort || s.Port > maxPort {
		return fmt.Errorf("server.port must be between %d and %d, got %d", minPort, maxPort, s.Port)
	}
	if err := checkDuration("server.session_ttl", s.SessionTTL, maxDuration); err != nil {
		return err
	}
	if err := checkDuration("server.idle_lock", s.IdleLock, maxDuration); err != nil {
		return err
	}
	if s.IdleLock > s.SessionTTL {
		return fmt.Errorf("server.idle_lock (%s) must not exceed server.session_ttl (%s)", s.IdleLock, s.SessionTTL)
	}
	return nil
}

func (c Cloudflare) validate(access Access) error {
	if c.Binary == "" {
		return errors.New("cloudflare.binary must not be empty")
	}
	if err := checkDuration("cloudflare.grace_period", c.GracePeriod, maxGracePeriod); err != nil {
		return err
	}
	for i, arg := range c.TokenCommand {
		if strings.TrimSpace(arg) == "" {
			return fmt.Errorf("cloudflare.token_command[%d] must not be empty", i)
		}
	}
	if c.PublicURL != "" {
		if err := validateHTTPSURL(c.PublicURL); err != nil {
			return fmt.Errorf("cloudflare.public_url: %w", err)
		}
	}
	switch c.Mode {
	case ModeNamed:
		return c.validateNamed(access)
	case ModeQuick:
		if !c.QuickEnabled {
			return errors.New("cloudflare.mode is \"quick\" but cloudflare.quick_enabled is false; Quick Tunnels must be explicitly enabled")
		}
		return nil
	default:
		return fmt.Errorf("cloudflare.mode must be %q or %q, got %q", ModeNamed, ModeQuick, c.Mode)
	}
}

func (c Cloudflare) validateNamed(access Access) error {
	if !access.Enabled {
		return errors.New("named mode requires auth.access.enabled = true (Cloudflare Access is the edge identity layer)")
	}
	if c.PublicURL == "" {
		return errors.New("named mode requires cloudflare.public_url (an absolute https URL)")
	}
	strategies := c.credentialStrategies()
	switch len(strategies) {
	case 1:
		// exactly one, as required.
	case 0:
		return errors.New("named mode requires exactly one cloudflared credential strategy: config/credentials, token file, or token command")
	default:
		return fmt.Errorf("named mode requires exactly one cloudflared credential strategy, but %d are configured: %s", len(strategies), strings.Join(strategies, ", "))
	}
	if err := access.validate(); err != nil {
		return err
	}
	// Named mode is Access-only: the Access JWT is the sole interactive gate, so
	// allowed_identities is the last identity filter the origin applies. An empty
	// one delegates the entire front door to the Access policy, which is only safe
	// if that policy is itself as tight as an SSH login - so it has to be a
	// deliberate declaration, never a default.
	if err := access.validateIdentityGate(); err != nil {
		return err
	}
	return nil
}

// credentialStrategies returns the distinct configured credential strategies.
func (c Cloudflare) credentialStrategies() []string {
	var s []string
	if c.ConfigFile != "" || c.CredentialsFile != "" || c.Tunnel != "" {
		s = append(s, "config/credentials")
	}
	if c.TokenFile != "" {
		s = append(s, "token file")
	}
	if len(c.TokenCommand) > 0 {
		s = append(s, "token command")
	}
	return s
}

func (a Access) validate() error {
	if err := checkDuration("auth.access.jwks_ttl", a.JWKSTTL, maxDuration); err != nil {
		return err
	}
	if a.TeamDomain == "" {
		return errors.New("auth.access.team_domain must not be empty when Access is enabled")
	}
	if err := validateTeamDomain(a.TeamDomain); err != nil {
		return err
	}
	if strings.TrimSpace(a.Audience) == "" {
		return errors.New("auth.access.audience must not be empty when Access is enabled (JWT validation requires the application audience)")
	}
	for i, id := range a.AllowedIdentities {
		if strings.TrimSpace(id) == "" {
			return fmt.Errorf("auth.access.allowed_identities[%d] must not be empty", i)
		}
		if len([]rune(id)) > maxIdentityRuneLen || containsControl(id) {
			return fmt.Errorf("auth.access.allowed_identities[%d] is not a valid identity", i)
		}
	}
	return nil
}

// HasIdentityAllowlist reports whether an exact-match identity allowlist is
// configured. When it is false in named mode, every identity the Cloudflare
// Access policy admits is accepted by the origin.
func (a Access) HasIdentityAllowlist() bool {
	for _, id := range a.AllowedIdentities {
		if strings.TrimSpace(id) != "" {
			return true
		}
	}
	return false
}

// validateIdentityGate enforces that named mode names the identities allowed to
// reach the relay, unless the operator has deliberately opted out. It is called
// only from validateNamed: quick mode has no edge identity at all, so the
// single-use pairing secret remains its gate and this setting is irrelevant
// there.
func (a Access) validateIdentityGate() error {
	if a.HasIdentityAllowlist() || a.AllowAnyIdentity {
		return nil
	}
	return errors.New("named mode requires a non-empty auth.access.allowed_identities: " +
		"Cloudflare Access is the only interactive gate in named mode, so this allowlist is the " +
		"last identity filter the origin applies, and leaving it empty grants every identity your " +
		"Access policy admits a shell-equivalent session. " +
		`List the exact verified email or common_name allowed to reach this relay, e.g. ` +
		`allowed_identities = ["you@example.com"], ` +
		"or set auth.access.allow_any_identity = true to accept every identity your Access policy " +
		"admits as a deliberate choice")
}

func (h Herdr) validate() error {
	if err := checkDuration("herdr.poll_hot", h.PollHot, maxPollInterval); err != nil {
		return err
	}
	if h.PollHot < minPollHot {
		return fmt.Errorf("herdr.poll_hot must be at least %s, got %s", minPollHot, h.PollHot)
	}
	if err := checkDuration("herdr.poll_cold", h.PollCold, maxPollInterval); err != nil {
		return err
	}
	if h.PollCold < h.PollHot {
		return fmt.Errorf("herdr.poll_cold (%s) must not be shorter than herdr.poll_hot (%s)", h.PollCold, h.PollHot)
	}
	return nil
}

func (u UI) validate() error {
	switch u.Theme {
	case ThemeSystem, ThemeLight, ThemeDark:
	default:
		return fmt.Errorf("ui.theme must be one of %q, %q, or %q, got %q", ThemeSystem, ThemeLight, ThemeDark, u.Theme)
	}
	if u.TerminalFontSize < minTerminalFont || u.TerminalFontSize > maxTerminalFont {
		return fmt.Errorf("ui.terminal_font_size must be between %d and %d, got %d", minTerminalFont, maxTerminalFont, u.TerminalFontSize)
	}
	return nil
}

// validate checks the experimental section.
//
// The parser list and the turn bound are validated even when the feature is off,
// so a mistake surfaces at start rather than the first time somebody flips the
// flag on and finds the relay refuses to boot.
func (e Experimental) validate() error {
	for _, name := range e.AgentOutputParsers {
		if !interpret.Supported(name) {
			return fmt.Errorf(
				"experimental.agent_output_parsers contains unknown agent kind %q; supported: %s",
				name, strings.Join(supportedParserNames(), ", "),
			)
		}
	}
	if e.AgentOutputParsing && len(e.AgentOutputParsers) == 0 {
		return errors.New("experimental.agent_output_parsing is true but agent_output_parsers is empty; nothing would be parsed")
	}
	if e.MaxInterpretedTurns < 1 || e.MaxInterpretedTurns > maxInterpretedTurn {
		return fmt.Errorf(
			"experimental.max_interpreted_turns must be between 1 and %d, got %d",
			maxInterpretedTurn, e.MaxInterpretedTurns,
		)
	}
	return nil
}

// ParsesAgentKind reports whether interpretation is enabled for one agent kind.
// A pane running anything else is never parsed (SPEC §12.2).
func (e Experimental) ParsesAgentKind(kind string) bool {
	if !e.AgentOutputParsing || kind == "" {
		return false
	}
	return slices.Contains(e.AgentOutputParsers, kind)
}

func supportedParserNames() []string {
	kinds := interpret.ParserKinds()
	out := make([]string, 0, len(kinds))
	for _, k := range kinds {
		out = append(out, string(k))
	}
	return out
}

func checkDuration(field string, d, max time.Duration) error {
	if d <= 0 {
		return fmt.Errorf("%s must be positive, got %s", field, d)
	}
	if d > max {
		return fmt.Errorf("%s must be at most %s, got %s", field, max, d)
	}
	return nil
}

func validateHTTPSURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("invalid URL %q: %v", raw, err)
	}
	if u.Scheme != "https" {
		return fmt.Errorf("must use https, got %q", raw)
	}
	if u.Host == "" || u.Hostname() == "" {
		return fmt.Errorf("must be absolute with a host, got %q", raw)
	}
	if u.User != nil {
		return fmt.Errorf("must not contain userinfo, got %q", raw)
	}
	return nil
}

// validateTeamDomain checks that s looks like a bare Access team hostname
// (for example "example.cloudflareaccess.com"): no scheme, path, or whitespace.
func validateTeamDomain(s string) error {
	if strings.ContainsAny(s, "/ \t") || strings.Contains(s, "://") {
		return fmt.Errorf("auth.access.team_domain must be a bare hostname (no scheme or path), got %q", s)
	}
	if !strings.Contains(s, ".") {
		return fmt.Errorf("auth.access.team_domain must be a fully qualified hostname, got %q", s)
	}
	return nil
}

func containsControl(s string) bool {
	for _, r := range s {
		if r < 0x20 || r == 0x7f {
			return true
		}
	}
	return false
}
