package auth

import (
	"bytes"
	"encoding/base64"
	"errors"
	"strings"
	"testing"
)

func TestPairing_TokenIs256Bit(t *testing.T) {
	t.Parallel()
	p, err := NewPairing()
	if err != nil {
		t.Fatalf("NewPairing: %v", err)
	}
	raw, err := base64.RawURLEncoding.DecodeString(p.Token())
	if err != nil {
		t.Fatalf("decode token: %v", err)
	}
	if len(raw) != 32 {
		t.Fatalf("token bytes = %d, want 32", len(raw))
	}
}

func TestPairing_VerifySingleUseAndRotate(t *testing.T) {
	t.Parallel()
	p, err := NewPairing()
	if err != nil {
		t.Fatalf("NewPairing: %v", err)
	}
	tok := p.Token()

	if !p.Verify(tok) {
		t.Fatal("first Verify should succeed")
	}
	// Single use: the same token must not pair again (secret rotated).
	if p.Verify(tok) {
		t.Fatal("second Verify with same token should fail (single use)")
	}
	// The new token works once.
	tok2 := p.Token()
	if tok2 == tok {
		t.Fatal("token should have rotated after successful pairing")
	}
	if !p.Verify(tok2) {
		t.Fatal("rotated token should verify")
	}
}

func TestPairing_VerifyRejectsWrong(t *testing.T) {
	t.Parallel()
	p, err := NewPairing()
	if err != nil {
		t.Fatalf("NewPairing: %v", err)
	}
	for _, bad := range []string{"", "AAAA", "!!!not-base64!!!", strings.Repeat("A", 43)} {
		if p.Verify(bad) {
			t.Fatalf("Verify(%q) should be false", bad)
		}
	}
	// A wrong secret of correct length must fail without rotating.
	before := p.Token()
	wrong := base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{0x01}, 32))
	if p.Verify(wrong) {
		t.Fatal("wrong secret should not verify")
	}
	if p.Token() != before {
		t.Fatal("secret must not rotate on failed verify")
	}
}

func TestPairing_Rotate(t *testing.T) {
	t.Parallel()
	p, err := NewPairing()
	if err != nil {
		t.Fatalf("NewPairing: %v", err)
	}
	old := p.Token()
	if err := p.Rotate(); err != nil {
		t.Fatalf("Rotate: %v", err)
	}
	if p.Token() == old {
		t.Fatal("Rotate should change the secret")
	}
	if p.Verify(old) {
		t.Fatal("old token must not verify after rotate")
	}
}

func TestPairing_Link(t *testing.T) {
	t.Parallel()
	p, err := NewPairing()
	if err != nil {
		t.Fatalf("NewPairing: %v", err)
	}
	link := p.Link("https://herdr.example.com/")
	if !strings.HasPrefix(link, "https://herdr.example.com/#pair=") {
		t.Fatalf("unexpected link: %q", link)
	}
	// The secret must live only in the fragment.
	if strings.Contains(link, "?") {
		t.Fatalf("secret must not be a query parameter: %q", link)
	}
	frag := strings.TrimPrefix(link, "https://herdr.example.com/#pair=")
	if frag != p.Token() {
		t.Fatalf("fragment token mismatch")
	}
}

// A deterministic reader lets us assert entropy is actually consumed.
type zeroReader struct{}

func (zeroReader) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = 0
	}
	return len(p), nil
}

func TestPairing_CustomRand(t *testing.T) {
	t.Parallel()
	p, err := NewPairing(WithRand(zeroReader{}))
	if err != nil {
		t.Fatalf("NewPairing: %v", err)
	}
	want := base64.RawURLEncoding.EncodeToString(make([]byte, 32))
	if p.Token() != want {
		t.Fatalf("token = %q, want all-zero", p.Token())
	}
}

// countingReader yields ok successful full reads, then errors — modeling an
// entropy source that fails after initialization. Each successful read fills a
// distinct byte pattern so successive secrets differ.
type countingReader struct {
	ok int
	n  byte
}

func (r *countingReader) Read(p []byte) (int, error) {
	if r.ok <= 0 {
		return 0, errors.New("entropy depleted")
	}
	r.ok--
	r.n++
	for i := range p {
		p[i] = r.n
	}
	return len(p), nil
}

func TestPairing_FailsClosedOnRotationEntropyFailure(t *testing.T) {
	t.Parallel()
	// One good read seeds the initial secret; the rotation after a successful
	// match then hits the depleted source.
	p, err := NewPairing(WithRand(&countingReader{ok: 1}))
	if err != nil {
		t.Fatalf("NewPairing: %v", err)
	}
	tok := p.Token()

	// The token matches, but the single-use rotation cannot draw entropy, so
	// Verify must fail closed rather than report success.
	if p.Verify(tok) {
		t.Fatal("Verify must return false when the single-use rotation entropy fails")
	}
	// The pairing is now invalidated: the just-matched token cannot be replayed.
	if p.Verify(tok) {
		t.Fatal("pairing must stay invalidated after a rotation entropy failure")
	}
	// Rotate with the still-broken source surfaces the entropy error and keeps
	// the pairing unusable.
	if err := p.Rotate(); !errors.Is(err, ErrEntropy) {
		t.Fatalf("Rotate err = %v, want ErrEntropy", err)
	}
	if p.Verify(tok) {
		t.Fatal("pairing must remain unusable until a successful rotation")
	}
}

func TestPairing_RecoversAfterInvalidation(t *testing.T) {
	t.Parallel()
	// Two good reads: init, then a successful rotation on the matching Verify.
	p, err := NewPairing(WithRand(&countingReader{ok: 2}))
	if err != nil {
		t.Fatalf("NewPairing: %v", err)
	}
	tok := p.Token()
	if !p.Verify(tok) {
		t.Fatal("Verify should succeed when rotation entropy is available")
	}
	// The rotated token is fresh and single-use.
	if tok == p.Token() {
		t.Fatal("secret should have rotated")
	}
}
