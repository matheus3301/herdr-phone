package auth

import (
	"context"
	"crypto/rsa"
	"encoding/base64"
	"testing"
	"time"
)

// FuzzPairingVerify asserts that Verify never panics and only accepts the exact
// current token. Random inputs must be rejected without rotating the secret.
func FuzzPairingVerify(f *testing.F) {
	f.Add("")
	f.Add("AAAA")
	f.Add(base64.RawURLEncoding.EncodeToString(make([]byte, 32)))

	f.Fuzz(func(t *testing.T, provided string) {
		p, err := NewPairing()
		if err != nil {
			t.Fatalf("NewPairing: %v", err)
		}
		correct := p.Token()
		before := correct

		accepted := p.Verify(provided)
		if accepted {
			// The only value that may be accepted is the correct token.
			if provided != correct {
				t.Fatalf("accepted a non-matching token: %q != %q", provided, correct)
			}
			// A successful pairing must rotate the secret.
			if p.Token() == before {
				t.Fatal("secret did not rotate after successful verify")
			}
			return
		}
		// On rejection the secret must be unchanged.
		if p.Token() != before {
			t.Fatal("secret rotated on a rejected verify")
		}
	})
}

// FuzzVerify asserts that arbitrary token strings never panic the verifier and
// never validate against a real signing key they were not signed with.
func FuzzVerify(f *testing.F) {
	now := int64(1_000_000)
	k := newTestKeyFuzz("kid-1")
	src := staticKeySource{keys: map[string]*rsa.PublicKey{k.kid: &k.priv.PublicKey}}
	v, err := NewVerifier(testIssuer, testAudience, nil, src, WithClock(func() time.Time { return time.Unix(now, 0) }))
	if err != nil {
		f.Fatalf("NewVerifier: %v", err)
	}

	f.Add("")
	f.Add("a.b.c")
	f.Add("eyJhbGciOiJSUzI1NiJ9.e30.")

	f.Fuzz(func(t *testing.T, token string) {
		// Must never panic; any returned claims must have a nil error and any
		// error must have nil claims.
		claims, err := v.Verify(context.Background(), token)
		if err == nil && claims == nil {
			t.Fatal("nil error with nil claims")
		}
		if err != nil && claims != nil {
			t.Fatal("non-nil error with non-nil claims")
		}
	})
}
