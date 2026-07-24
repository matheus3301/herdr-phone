package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"io"
	"strings"
	"sync"
)

// pairingSecretBytes is the pairing-secret size: 256 bits of entropy.
const pairingSecretBytes = 32

// Pairing holds the current single-use pairing secret for a daemon instance.
// The secret is delivered out of band via a URL fragment and exchanged once for
// a session. A successful verification rotates the secret so it can never be
// replayed.
type Pairing struct {
	mu     sync.Mutex
	secret []byte
	rand   io.Reader
	// invalidated is set when a successful pairing could not be followed by a
	// secret rotation (entropy failure). Once set, no Verify can succeed until a
	// fresh Rotate restores a usable, rotatable secret — this keeps the
	// single-use guarantee fail-closed.
	invalidated bool
}

// PairingOption customizes a Pairing.
type PairingOption func(*Pairing)

// WithRand overrides the entropy source (for tests).
func WithRand(r io.Reader) PairingOption {
	return func(p *Pairing) {
		if r != nil {
			p.rand = r
		}
	}
}

// NewPairing creates a Pairing with a fresh random secret.
func NewPairing(opts ...PairingOption) (*Pairing, error) {
	p := &Pairing{rand: rand.Reader}
	for _, o := range opts {
		o(p)
	}
	if err := p.rotateLocked(); err != nil {
		return nil, err
	}
	return p, nil
}

func (p *Pairing) rotateLocked() error {
	buf := make([]byte, pairingSecretBytes)
	if _, err := io.ReadFull(p.rand, buf); err != nil {
		return ErrEntropy
	}
	p.secret = buf
	p.invalidated = false
	return nil
}

// Rotate replaces the pairing secret with a new random value, invalidating any
// previously issued pairing link. setup-link uses this.
func (p *Pairing) Rotate() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.rotateLocked()
}

// Token returns the current secret encoded as base64url, for embedding in a
// pairing link fragment.
func (p *Pairing) Token() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return base64.RawURLEncoding.EncodeToString(p.secret)
}

// Verify reports whether provided matches the current pairing secret. The
// comparison is constant-time and length-independent. On a match the secret is
// rotated (single use) before returning true; on a mismatch the secret is left
// unchanged and false is returned.
//
// Fail-closed on rotation entropy failure: if the secret matches but the
// mandatory single-use rotation cannot draw fresh entropy, Verify invalidates
// the pairing and returns false rather than reporting success while leaving the
// just-used secret replayable. Recovery requires an explicit Rotate.
func (p *Pairing) Verify(provided string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.invalidated {
		// Spend a constant-time comparison to keep timing uniform, then deny.
		subtle.ConstantTimeCompare(p.secret, p.secret)
		return false
	}

	decoded, err := base64.RawURLEncoding.DecodeString(strings.TrimSpace(provided))
	if err != nil {
		// Still spend a constant-time comparison against the real secret to
		// avoid leaking, via timing, whether decoding failed versus mismatched.
		subtle.ConstantTimeCompare(p.secret, p.secret)
		return false
	}

	if subtle.ConstantTimeCompare(decoded, p.secret) != 1 {
		return false
	}

	// Single use: rotate so the same link cannot pair twice. If rotation fails
	// (entropy exhaustion), we cannot uphold the single-use guarantee, so we
	// invalidate the pairing and fail closed for this exchange too.
	if err := p.rotateLocked(); err != nil {
		p.invalidated = true
		return false
	}
	return true
}

// Link builds the pairing URL for the current secret. The secret travels only
// in the URL fragment, which browsers never send in HTTP requests.
func Link(baseURL, token string) string {
	base := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	return base + "/#pair=" + token
}

// Link returns the pairing URL for the current secret against baseURL.
func (p *Pairing) Link(baseURL string) string {
	return Link(baseURL, p.Token())
}
