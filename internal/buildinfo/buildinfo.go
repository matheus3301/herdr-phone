// Package buildinfo is the single source of truth for herdr-phone identity and
// version. The plugin manifest, the release tag, and the Herdr compatibility
// check are all validated against these constants (enforced by tests), so a
// version or name only ever changes in one place.
package buildinfo

// Version is the semantic version of herdr-phone. It must match the version
// field in herdr-plugin.toml and the release tag (enforced by tests).
const Version = "0.1.0"

// Name is the program and binary name.
const Name = "herdr-phone"

// DisplayName is the human-facing plugin/product name shown in Herdr and docs.
const DisplayName = "Herdr Phone"

// MinHerdrVersion is the minimum Herdr version this plugin supports. It is the
// manifest's min_herdr_version and the floor the daemon enforces on ping.
const MinHerdrVersion = "0.7.5"

// HerdrProtocol is the Herdr wire protocol this plugin is built against. The
// daemon verifies the live server reports this protocol on ping before trusting
// any other response (unknown response fields are tolerated).
const HerdrProtocol = 17

// UserAgent is the HTTP User-Agent used for outbound requests the plugin itself
// makes (for example Cloudflare Access JWKS fetches). It is versioned so traffic
// is attributable to this plugin.
const UserAgent = Name + "/" + Version + " (+https://github.com/matheus3301/herdr-phone)"
