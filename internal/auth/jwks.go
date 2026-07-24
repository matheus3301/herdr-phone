package auth

import (
	"context"
	"crypto/rsa"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

// DefaultJWKSTTL is the cache lifetime for fetched JWKS keys.
const DefaultJWKSTTL = time.Hour

// defaultJWKSMaxBody bounds the JWKS response body to defend against a hostile
// or buggy endpoint returning an unbounded stream.
const defaultJWKSMaxBody int64 = 1 << 20 // 1 MiB

// defaultJWKSTimeout bounds a single JWKS fetch.
const defaultJWKSTimeout = 10 * time.Second

// defaultMinRefreshInterval throttles refreshes triggered by an unknown kid
// while the cache is still fresh. A stream of tokens carrying random kids (only
// reachable via direct origin access, behind Access) must not drive one outbound
// JWKS GET per request and risk upstream rate-limiting.
const defaultMinRefreshInterval = 15 * time.Second

// ErrJWKSUnavailable is returned when keys cannot be fetched and no acceptable
// cached key exists. Verification then fails closed.
var ErrJWKSUnavailable = errors.New("auth: JWKS unavailable and no valid cached key")

// JWKSCache fetches and caches Cloudflare Access signing keys from a team's
// certs endpoint. It bounds each fetch, coalesces concurrent refreshes with
// singleflight, and serves stale keys for at most one additional TTL when a
// refresh fails.
type JWKSCache struct {
	url                string
	client             *http.Client
	ttl                time.Duration
	maxBody            int64
	minRefreshInterval time.Duration
	now                func() time.Time

	sf singleflightGroup

	mu          sync.RWMutex
	keys        map[string]*rsa.PublicKey
	fetchedAt   time.Time
	lastAttempt time.Time
	loaded      bool
}

// JWKSOption customizes a JWKSCache.
type JWKSOption func(*JWKSCache)

// WithHTTPClient overrides the HTTP client (for tests or custom transports).
func WithHTTPClient(c *http.Client) JWKSOption {
	return func(j *JWKSCache) {
		if c != nil {
			j.client = c
		}
	}
}

// WithTTL overrides the cache TTL.
func WithTTL(d time.Duration) JWKSOption {
	return func(j *JWKSCache) {
		if d > 0 {
			j.ttl = d
		}
	}
}

// WithMinRefreshInterval overrides the unknown-kid refresh throttle window.
func WithMinRefreshInterval(d time.Duration) JWKSOption {
	return func(j *JWKSCache) {
		if d >= 0 {
			j.minRefreshInterval = d
		}
	}
}

// WithNow overrides the time source (for tests).
func WithNow(now func() time.Time) JWKSOption {
	return func(j *JWKSCache) {
		if now != nil {
			j.now = now
		}
	}
}

// WithMaxBody overrides the JWKS response body limit.
func WithMaxBody(n int64) JWKSOption {
	return func(j *JWKSCache) {
		if n > 0 {
			j.maxBody = n
		}
	}
}

// WithCertsURL overrides the derived certs URL (for tests).
func WithCertsURL(u string) JWKSOption {
	return func(j *JWKSCache) {
		if u != "" {
			j.url = u
		}
	}
}

// NormalizeTeamDomain returns the bare host of a Cloudflare team domain,
// accepting inputs with or without a scheme or trailing slash.
func NormalizeTeamDomain(teamDomain string) string {
	d := strings.TrimSpace(teamDomain)
	d = strings.TrimPrefix(d, "https://")
	d = strings.TrimPrefix(d, "http://")
	d = strings.TrimSuffix(d, "/")
	return strings.ToLower(d)
}

// IssuerForTeam returns the Access token issuer URL for a team domain.
func IssuerForTeam(teamDomain string) string {
	return "https://" + NormalizeTeamDomain(teamDomain)
}

// CertsURLForTeam returns the JWKS certs endpoint for a team domain.
func CertsURLForTeam(teamDomain string) string {
	return IssuerForTeam(teamDomain) + "/cdn-cgi/access/certs"
}

// NewJWKSCache builds a cache for a team domain (e.g. "example.cloudflareaccess.com").
func NewJWKSCache(teamDomain string, opts ...JWKSOption) (*JWKSCache, error) {
	host := NormalizeTeamDomain(teamDomain)
	if host == "" {
		return nil, errors.New("auth: team domain is required")
	}
	j := &JWKSCache{
		url:                CertsURLForTeam(teamDomain),
		client:             &http.Client{Timeout: defaultJWKSTimeout},
		ttl:                DefaultJWKSTTL,
		maxBody:            defaultJWKSMaxBody,
		minRefreshInterval: defaultMinRefreshInterval,
		now:                time.Now,
		keys:               map[string]*rsa.PublicKey{},
	}
	for _, o := range opts {
		o(j)
	}
	return j, nil
}

// PublicKey returns the RSA public key for kid, refreshing the cache when the
// cached keys are stale or the kid is unknown. It fails closed when a refresh
// fails and no acceptable cached key exists.
func (j *JWKSCache) PublicKey(ctx context.Context, kid string) (*rsa.PublicKey, error) {
	j.mu.RLock()
	key, present := j.keys[kid]
	now := j.now()
	fresh := j.loaded && now.Sub(j.fetchedAt) < j.ttl
	sinceAttempt := now.Sub(j.lastAttempt)
	j.mu.RUnlock()

	if present && fresh {
		return key, nil
	}

	// Unknown kid while the cache is still fresh: a rotation may have added the
	// key, so a refresh is warranted — but throttle it so a flood of random
	// kids cannot storm the JWKS endpoint. Within the throttle window, fail
	// closed for this token without a fetch.
	if fresh && !present && sinceAttempt < j.minRefreshInterval {
		return nil, ErrUnknownKID
	}

	// Refresh (coalesced). A stale-but-present kid still triggers a refresh so
	// rotated keys are picked up, but can fall back to the stale key on failure.
	_, refreshErr, _ := j.sf.Do("fetch", func() (any, error) {
		return nil, j.refresh(ctx)
	})

	j.mu.RLock()
	key, present = j.keys[kid]
	age := j.now().Sub(j.fetchedAt)
	loaded := j.loaded
	j.mu.RUnlock()

	if refreshErr == nil {
		if present {
			return key, nil
		}
		return nil, ErrUnknownKID
	}

	// Refresh failed: serve a stale key for at most one extra TTL beyond expiry.
	if present && loaded && age <= 2*j.ttl {
		return key, nil
	}
	return nil, fmt.Errorf("%w: %v", ErrJWKSUnavailable, refreshErr)
}

type jwk struct {
	Kty string `json:"kty"`
	Kid string `json:"kid"`
	N   string `json:"n"`
	E   string `json:"e"`
	Alg string `json:"alg"`
	Use string `json:"use"`
}

type jwksDocument struct {
	Keys []jwk `json:"keys"`
	// Cloudflare also mirrors keys under public_certs / public_cert; we ignore
	// those deliberately and only trust the standard JWKS "keys" set matched by
	// kid, per Cloudflare's validation guidance.
}

func (j *JWKSCache) refresh(ctx context.Context) error {
	// Record the attempt time up front (success or failure) so the unknown-kid
	// throttle in PublicKey measures from the last actual fetch attempt.
	j.mu.Lock()
	j.lastAttempt = j.now()
	j.mu.Unlock()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, j.url, nil)
	if err != nil {
		return fmt.Errorf("auth: build JWKS request: %w", err)
	}
	req.Header.Set("Accept", "application/json")

	resp, err := j.client.Do(req)
	if err != nil {
		return fmt.Errorf("auth: fetch JWKS: %w", err)
	}
	defer func() {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, j.maxBody))
		_ = resp.Body.Close()
	}()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("auth: JWKS endpoint returned status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, j.maxBody))
	if err != nil {
		return fmt.Errorf("auth: read JWKS body: %w", err)
	}

	var doc jwksDocument
	if err := json.Unmarshal(body, &doc); err != nil {
		return fmt.Errorf("auth: parse JWKS: %w", err)
	}

	next := make(map[string]*rsa.PublicKey, len(doc.Keys))
	for _, k := range doc.Keys {
		if !strings.EqualFold(k.Kty, "RSA") {
			continue
		}
		if k.Alg != "" && !strings.EqualFold(k.Alg, "RS256") {
			continue
		}
		if k.Use != "" && !strings.EqualFold(k.Use, "sig") {
			continue
		}
		if k.Kid == "" {
			continue
		}
		pub, err := rsaPublicKeyFromJWK(k.N, k.E)
		if err != nil {
			continue
		}
		next[k.Kid] = pub
	}

	if len(next) == 0 {
		return errors.New("auth: JWKS contained no usable RSA signing keys")
	}

	j.mu.Lock()
	j.keys = next
	j.fetchedAt = j.now()
	j.loaded = true
	j.mu.Unlock()
	return nil
}
