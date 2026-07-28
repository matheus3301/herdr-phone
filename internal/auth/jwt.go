// Package auth implements the origin-side authentication for herdr-phone:
// Cloudflare Access JWT validation (RS256 with JWKS), single-use pairing, the
// in-memory HttpOnly session lifecycle, and CSRF tokens. Every check fails
// closed: on any error a caller is treated as unauthenticated.
package auth

import (
	"context"
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"time"
)

// Verification errors. Callers should treat any non-nil error from Verify as a
// hard authentication failure and must not distinguish reasons to a client.
var (
	ErrMalformedToken = errors.New("auth: malformed access token")
	ErrUnsupportedAlg = errors.New("auth: unsupported token algorithm")
	ErrMissingKID     = errors.New("auth: token missing key id")
	ErrUnknownKID     = errors.New("auth: unknown signing key")
	ErrBadSignature   = errors.New("auth: invalid token signature")
	ErrExpired        = errors.New("auth: token expired")
	ErrNotYetValid    = errors.New("auth: token not yet valid")
	ErrIssuedInFuture = errors.New("auth: token issued in the future")
	ErrWrongIssuer    = errors.New("auth: token issuer mismatch")
	ErrWrongAudience  = errors.New("auth: token audience mismatch")
	ErrIdentityDenied = errors.New("auth: identity not permitted")
	ErrNoIdentity     = errors.New("auth: token carries no identity claim")
)

// DefaultClockSkew is the tolerance applied to time-based claims.
const DefaultClockSkew = 60 * time.Second

// minRSAModulusBits is the smallest RSA modulus accepted from a JWKS. Cloudflare
// Access signs with 2048-bit RSA; anything smaller is rejected as unsafe.
const minRSAModulusBits = 2048

var b64url = base64.RawURLEncoding

// KeySource resolves an RSA public key by its JWKS key id.
type KeySource interface {
	PublicKey(ctx context.Context, kid string) (*rsa.PublicKey, error)
}

// Claims are the subset of Cloudflare Access token claims used by the origin.
type Claims struct {
	Issuer     string
	Audience   []string
	Email      string
	CommonName string
	Subject    string
	Type       string
	IssuedAt   int64
	NotBefore  int64
	ExpiresAt  int64
}

// Identity returns the verified operator identity: the email for interactive
// logins, or the common_name for service-token logins. It returns an empty
// string only when neither claim is present.
func (c *Claims) Identity() string {
	if c.Email != "" {
		return c.Email
	}
	return c.CommonName
}

// Verifier validates Cloudflare Access JWTs presented at the origin. It accepts
// only RS256, requires an exact issuer and audience match, enforces the time
// claims with a bounded skew, and — when a non-empty allowlist is configured —
// requires the verified email or common_name to match exactly.
type Verifier struct {
	issuer   string
	audience string
	allowed  map[string]struct{}
	keys     KeySource
	now      func() time.Time
	skew     time.Duration
}

// VerifierOption customizes a Verifier.
type VerifierOption func(*Verifier)

// WithClock overrides the time source (for tests).
func WithClock(now func() time.Time) VerifierOption {
	return func(v *Verifier) {
		if now != nil {
			v.now = now
		}
	}
}

// WithClockSkew overrides the time-claim tolerance.
func WithClockSkew(d time.Duration) VerifierOption {
	return func(v *Verifier) {
		if d >= 0 {
			v.skew = d
		}
	}
}

// NewVerifier builds a Verifier. The issuer and audience must be non-empty; the
// allowed slice may be empty, in which case any Access-authenticated identity is
// accepted. Identities are matched exactly.
func NewVerifier(issuer, audience string, allowed []string, keys KeySource, opts ...VerifierOption) (*Verifier, error) {
	issuer = strings.TrimSpace(issuer)
	audience = strings.TrimSpace(audience)
	if issuer == "" {
		return nil, errors.New("auth: issuer is required")
	}
	if audience == "" {
		return nil, errors.New("auth: audience is required")
	}
	if keys == nil {
		return nil, errors.New("auth: key source is required")
	}
	set := make(map[string]struct{}, len(allowed))
	for _, a := range allowed {
		a = strings.TrimSpace(a)
		if a != "" {
			set[a] = struct{}{}
		}
	}
	v := &Verifier{
		issuer:   issuer,
		audience: audience,
		allowed:  set,
		keys:     keys,
		now:      time.Now,
		skew:     DefaultClockSkew,
	}
	for _, o := range opts {
		o(v)
	}
	return v, nil
}

type jwtHeader struct {
	Alg string `json:"alg"`
	Kid string `json:"kid"`
	Typ string `json:"typ"`
}

// audienceValue accepts a JWT "aud" claim encoded as either a string or an
// array of strings.
type audienceValue []string

func (a *audienceValue) UnmarshalJSON(data []byte) error {
	data = []byte(strings.TrimSpace(string(data)))
	if len(data) == 0 || string(data) == "null" {
		*a = nil
		return nil
	}
	if data[0] == '[' {
		var xs []string
		if err := json.Unmarshal(data, &xs); err != nil {
			return err
		}
		*a = xs
		return nil
	}
	var s string
	if err := json.Unmarshal(data, &s); err != nil {
		return err
	}
	*a = []string{s}
	return nil
}

type jwtClaims struct {
	Iss        string        `json:"iss"`
	Aud        audienceValue `json:"aud"`
	Email      string        `json:"email"`
	CommonName string        `json:"common_name"`
	Sub        string        `json:"sub"`
	Type       string        `json:"type"`
	Iat        int64         `json:"iat"`
	Nbf        int64         `json:"nbf"`
	Exp        int64         `json:"exp"`
}

// Verify parses and validates a compact JWT string. On success it returns the
// verified claims; on any failure it returns a wrapped sentinel error and nil
// claims.
func (v *Verifier) Verify(ctx context.Context, token string) (*Claims, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 || parts[0] == "" || parts[1] == "" || parts[2] == "" {
		return nil, ErrMalformedToken
	}

	headerBytes, err := b64url.DecodeString(parts[0])
	if err != nil {
		return nil, fmt.Errorf("%w: header: %v", ErrMalformedToken, err)
	}
	var hdr jwtHeader
	if err := json.Unmarshal(headerBytes, &hdr); err != nil {
		return nil, fmt.Errorf("%w: header: %v", ErrMalformedToken, err)
	}
	if hdr.Alg != "RS256" {
		return nil, ErrUnsupportedAlg
	}
	if hdr.Kid == "" {
		return nil, ErrMissingKID
	}

	pub, err := v.keys.PublicKey(ctx, hdr.Kid)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrUnknownKID, err)
	}
	if pub == nil {
		return nil, ErrUnknownKID
	}

	sig, err := b64url.DecodeString(parts[2])
	if err != nil {
		return nil, fmt.Errorf("%w: signature: %v", ErrMalformedToken, err)
	}
	signingInput := parts[0] + "." + parts[1]
	digest := sha256.Sum256([]byte(signingInput))
	if err := rsa.VerifyPKCS1v15(pub, crypto.SHA256, digest[:], sig); err != nil {
		return nil, ErrBadSignature
	}

	payloadBytes, err := b64url.DecodeString(parts[1])
	if err != nil {
		return nil, fmt.Errorf("%w: payload: %v", ErrMalformedToken, err)
	}
	var raw jwtClaims
	if err := json.Unmarshal(payloadBytes, &raw); err != nil {
		return nil, fmt.Errorf("%w: payload: %v", ErrMalformedToken, err)
	}

	now := v.now()
	skewSecs := int64(v.skew / time.Second)

	if raw.Exp == 0 {
		return nil, ErrExpired
	}
	if now.Unix() > raw.Exp+skewSecs {
		return nil, ErrExpired
	}
	if raw.Nbf != 0 && now.Unix()+skewSecs < raw.Nbf {
		return nil, ErrNotYetValid
	}
	if raw.Iat != 0 && raw.Iat > now.Unix()+skewSecs {
		return nil, ErrIssuedInFuture
	}

	if raw.Iss != v.issuer {
		return nil, ErrWrongIssuer
	}
	if !containsExact(raw.Aud, v.audience) {
		return nil, ErrWrongAudience
	}

	claims := &Claims{
		Issuer:     raw.Iss,
		Audience:   []string(raw.Aud),
		Email:      raw.Email,
		CommonName: raw.CommonName,
		Subject:    raw.Sub,
		Type:       raw.Type,
		IssuedAt:   raw.Iat,
		NotBefore:  raw.Nbf,
		ExpiresAt:  raw.Exp,
	}

	// The allowlist is enforced exactly and fails closed whenever one is
	// configured. An empty set accepts any Access-authenticated identity, which is
	// why named mode is only allowed to reach that state through an explicit
	// auth.access.allow_any_identity opt-out (see config.Access.validateIdentityGate)
	// - since named mode became Access-only, this is the last identity filter the
	// origin applies. NewVerifier drops blank entries, so a whitespace-only entry
	// never counts as an allowlist here either. Do not relax this to "skip when
	// unset" in any other way.
	if len(v.allowed) > 0 {
		identity := claims.Identity()
		if identity == "" {
			return nil, ErrNoIdentity
		}
		_, emailOK := v.allowed[claims.Email]
		_, cnOK := v.allowed[claims.CommonName]
		if !(claims.Email != "" && emailOK) && !(claims.CommonName != "" && cnOK) {
			return nil, ErrIdentityDenied
		}
	}

	return claims, nil
}

func containsExact(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

// rsaPublicKeyFromJWK builds an RSA public key from the base64url-encoded
// modulus (n) and exponent (e) of a JWK.
func rsaPublicKeyFromJWK(nB64, eB64 string) (*rsa.PublicKey, error) {
	nBytes, err := b64url.DecodeString(strings.TrimRight(nB64, "="))
	if err != nil {
		return nil, fmt.Errorf("auth: decode modulus: %w", err)
	}
	eBytes, err := b64url.DecodeString(strings.TrimRight(eB64, "="))
	if err != nil {
		return nil, fmt.Errorf("auth: decode exponent: %w", err)
	}
	if len(nBytes) == 0 || len(eBytes) == 0 {
		return nil, errors.New("auth: empty RSA key material")
	}
	n := new(big.Int).SetBytes(nBytes)
	e := new(big.Int).SetBytes(eBytes)
	if !e.IsInt64() || e.Int64() < 2 {
		return nil, errors.New("auth: invalid RSA exponent")
	}
	// Reject undersized moduli. A weak key from a compromised or misconfigured
	// JWKS must not be trusted even though the endpoint is reached over HTTPS.
	if n.BitLen() < minRSAModulusBits {
		return nil, fmt.Errorf("auth: RSA modulus too small: %d bits (minimum %d)", n.BitLen(), minRSAModulusBits)
	}
	return &rsa.PublicKey{N: n, E: int(e.Int64())}, nil
}
