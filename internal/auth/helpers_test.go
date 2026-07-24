package auth

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"testing"
)

// testKey is a small RSA key generated once per test binary run. 2048-bit keys
// are used to keep signing fast while remaining realistic.
type testKey struct {
	priv *rsa.PrivateKey
	kid  string
}

func newTestKey(t *testing.T, kid string) *testKey {
	t.Helper()
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	return &testKey{priv: priv, kid: kid}
}

// newTestKeyFuzz generates a key outside a *testing.T context (fuzz seed setup).
func newTestKeyFuzz(kid string) *testKey {
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		panic(err)
	}
	return &testKey{priv: priv, kid: kid}
}

// weakJWK renders a JWKS entry for an RSA key of the given bit size (used to
// verify that undersized moduli are rejected).
func weakJWK(t *testing.T, kid string, bits int) map[string]string {
	t.Helper()
	priv, err := rsa.GenerateKey(rand.Reader, bits)
	if err != nil {
		t.Fatalf("generate %d-bit key: %v", bits, err)
	}
	n := base64.RawURLEncoding.EncodeToString(priv.PublicKey.N.Bytes())
	e := base64.RawURLEncoding.EncodeToString(big.NewInt(int64(priv.PublicKey.E)).Bytes())
	return map[string]string{"kty": "RSA", "kid": kid, "alg": "RS256", "use": "sig", "n": n, "e": e}
}

// jwk renders the public key as a JWKS entry.
func (k *testKey) jwk() map[string]string {
	pub := k.priv.PublicKey
	n := base64.RawURLEncoding.EncodeToString(pub.N.Bytes())
	e := base64.RawURLEncoding.EncodeToString(big.NewInt(int64(pub.E)).Bytes())
	return map[string]string{
		"kty": "RSA",
		"kid": k.kid,
		"alg": "RS256",
		"use": "sig",
		"n":   n,
		"e":   e,
	}
}

// signToken builds a compact RS256 JWT with the given header alg/kid and claims.
func (k *testKey) signToken(t *testing.T, alg, kid string, claims map[string]any) string {
	t.Helper()
	header := map[string]string{"alg": alg, "typ": "JWT"}
	if kid != "" {
		header["kid"] = kid
	}
	hb, _ := json.Marshal(header)
	cb, _ := json.Marshal(claims)
	seg := base64.RawURLEncoding.EncodeToString(hb) + "." + base64.RawURLEncoding.EncodeToString(cb)
	digest := sha256.Sum256([]byte(seg))
	sig, err := rsa.SignPKCS1v15(rand.Reader, k.priv, crypto.SHA256, digest[:])
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	return seg + "." + base64.RawURLEncoding.EncodeToString(sig)
}

// staticKeySource resolves a fixed set of keys by kid.
type staticKeySource struct {
	keys map[string]*rsa.PublicKey
	err  error
}

func (s staticKeySource) PublicKey(_ context.Context, kid string) (*rsa.PublicKey, error) {
	if s.err != nil {
		return nil, s.err
	}
	k, ok := s.keys[kid]
	if !ok {
		return nil, ErrUnknownKID
	}
	return k, nil
}
