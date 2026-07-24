package auth

import (
	"context"
	"crypto/rsa"
	"errors"
	"testing"
	"time"
)

const (
	testIssuer   = "https://example.cloudflareaccess.com"
	testAudience = "aud-tag-1234567890abcdef"
)

func fixedClock(ts int64) func() time.Time {
	return func() time.Time { return time.Unix(ts, 0) }
}

func baseClaims(now int64) map[string]any {
	return map[string]any{
		"iss":   testIssuer,
		"aud":   []string{testAudience},
		"email": "operator@example.com",
		"sub":   "user-1",
		"iat":   now - 10,
		"nbf":   now - 10,
		"exp":   now + 3600,
	}
}

func newTestVerifier(t *testing.T, k *testKey, allowed []string, now int64) *Verifier {
	t.Helper()
	src := staticKeySource{keys: map[string]*rsa.PublicKey{k.kid: &k.priv.PublicKey}}
	v, err := NewVerifier(testIssuer, testAudience, allowed, src, WithClock(fixedClock(now)))
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}
	return v
}

func TestVerify_Valid(t *testing.T) {
	t.Parallel()
	now := int64(1_000_000)
	k := newTestKey(t, "kid-1")
	v := newTestVerifier(t, k, nil, now)

	tok := k.signToken(t, "RS256", "kid-1", baseClaims(now))
	claims, err := v.Verify(context.Background(), tok)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if claims.Email != "operator@example.com" {
		t.Fatalf("email = %q", claims.Email)
	}
	if claims.Identity() != "operator@example.com" {
		t.Fatalf("identity = %q", claims.Identity())
	}
}

func TestVerify_ServiceTokenCommonName(t *testing.T) {
	t.Parallel()
	now := int64(1_000_000)
	k := newTestKey(t, "kid-1")
	v := newTestVerifier(t, k, []string{"svc.example.com"}, now)

	c := baseClaims(now)
	delete(c, "email")
	c["sub"] = ""
	c["common_name"] = "svc.example.com"
	tok := k.signToken(t, "RS256", "kid-1", c)

	claims, err := v.Verify(context.Background(), tok)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if claims.Identity() != "svc.example.com" {
		t.Fatalf("identity = %q", claims.Identity())
	}
}

func TestVerify_Rejections(t *testing.T) {
	t.Parallel()
	now := int64(1_000_000)
	k := newTestKey(t, "kid-1")
	other := newTestKey(t, "kid-1") // same kid, different key

	tests := []struct {
		name    string
		mutate  func(c map[string]any)
		sign    func(t *testing.T) string
		allowed []string
		want    error
	}{
		{
			name: "wrong issuer",
			sign: func(t *testing.T) string {
				c := baseClaims(now)
				c["iss"] = "https://evil.cloudflareaccess.com"
				return k.signToken(t, "RS256", "kid-1", c)
			},
			want: ErrWrongIssuer,
		},
		{
			name: "wrong audience",
			sign: func(t *testing.T) string {
				c := baseClaims(now)
				c["aud"] = []string{"some-other-aud"}
				return k.signToken(t, "RS256", "kid-1", c)
			},
			want: ErrWrongAudience,
		},
		{
			name: "expired",
			sign: func(t *testing.T) string {
				c := baseClaims(now)
				c["exp"] = now - 3600
				return k.signToken(t, "RS256", "kid-1", c)
			},
			want: ErrExpired,
		},
		{
			name: "not yet valid",
			sign: func(t *testing.T) string {
				c := baseClaims(now)
				c["nbf"] = now + 3600
				return k.signToken(t, "RS256", "kid-1", c)
			},
			want: ErrNotYetValid,
		},
		{
			name: "issued in future",
			sign: func(t *testing.T) string {
				c := baseClaims(now)
				c["iat"] = now + 3600
				return k.signToken(t, "RS256", "kid-1", c)
			},
			want: ErrIssuedInFuture,
		},
		{
			name: "alg none",
			sign: func(t *testing.T) string {
				return k.signToken(t, "none", "kid-1", baseClaims(now))
			},
			want: ErrUnsupportedAlg,
		},
		{
			name: "missing kid",
			sign: func(t *testing.T) string {
				return k.signToken(t, "RS256", "", baseClaims(now))
			},
			want: ErrMissingKID,
		},
		{
			name: "bad signature (key mismatch)",
			sign: func(t *testing.T) string {
				return other.signToken(t, "RS256", "kid-1", baseClaims(now))
			},
			want: ErrBadSignature,
		},
		{
			name: "identity denied",
			sign: func(t *testing.T) string {
				return k.signToken(t, "RS256", "kid-1", baseClaims(now))
			},
			allowed: []string{"someone-else@example.com"},
			want:    ErrIdentityDenied,
		},
		{
			name: "malformed",
			sign: func(t *testing.T) string {
				return "not.a.valid.jwt.at.all"
			},
			want: ErrMalformedToken,
		},
		{
			name: "empty",
			sign: func(t *testing.T) string {
				return ""
			},
			want: ErrMalformedToken,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			v := newTestVerifier(t, k, tc.allowed, now)
			tok := tc.sign(t)
			_, err := v.Verify(context.Background(), tok)
			if !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestVerify_UnknownKIDFailsClosed(t *testing.T) {
	t.Parallel()
	now := int64(1_000_000)
	k := newTestKey(t, "kid-1")
	src := staticKeySource{keys: map[string]*rsa.PublicKey{"other-kid": &k.priv.PublicKey}}
	v, err := NewVerifier(testIssuer, testAudience, nil, src, WithClock(fixedClock(now)))
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}
	tok := k.signToken(t, "RS256", "kid-1", baseClaims(now))
	if _, err := v.Verify(context.Background(), tok); !errors.Is(err, ErrUnknownKID) {
		t.Fatalf("err = %v, want ErrUnknownKID", err)
	}
}

func TestVerify_StringAudience(t *testing.T) {
	t.Parallel()
	now := int64(1_000_000)
	k := newTestKey(t, "kid-1")
	v := newTestVerifier(t, k, nil, now)

	c := baseClaims(now)
	c["aud"] = testAudience // string form rather than array
	tok := k.signToken(t, "RS256", "kid-1", c)
	if _, err := v.Verify(context.Background(), tok); err != nil {
		t.Fatalf("Verify string aud: %v", err)
	}
}

func TestVerify_SkewTolerance(t *testing.T) {
	t.Parallel()
	now := int64(1_000_000)
	k := newTestKey(t, "kid-1")
	v := newTestVerifier(t, k, nil, now)

	c := baseClaims(now)
	c["exp"] = now - 30 // expired 30s ago, within 60s default skew
	tok := k.signToken(t, "RS256", "kid-1", c)
	if _, err := v.Verify(context.Background(), tok); err != nil {
		t.Fatalf("Verify within skew: %v", err)
	}
}

func TestRSAModulusMinimum(t *testing.T) {
	t.Parallel()
	// A 2048-bit key is accepted.
	strong := newTestKey(t, "strong").jwk()
	if _, err := rsaPublicKeyFromJWK(strong["n"], strong["e"]); err != nil {
		t.Fatalf("2048-bit key rejected: %v", err)
	}
	// A 1024-bit key is rejected as too small.
	weak := weakJWK(t, "weak", 1024)
	if _, err := rsaPublicKeyFromJWK(weak["n"], weak["e"]); err == nil {
		t.Fatal("1024-bit key must be rejected")
	}
}

func TestNewVerifier_Validation(t *testing.T) {
	t.Parallel()
	src := staticKeySource{keys: map[string]*rsa.PublicKey{}}
	if _, err := NewVerifier("", testAudience, nil, src); err == nil {
		t.Fatal("expected error for empty issuer")
	}
	if _, err := NewVerifier(testIssuer, "", nil, src); err == nil {
		t.Fatal("expected error for empty audience")
	}
	if _, err := NewVerifier(testIssuer, testAudience, nil, nil); err == nil {
		t.Fatal("expected error for nil key source")
	}
}
