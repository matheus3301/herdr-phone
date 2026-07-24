package server

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"sync"
	"time"
)

// randToken returns a cryptographically random URL-safe token of nbytes entropy.
func randToken(nbytes int) string {
	b := make([]byte, nbytes)
	if _, err := rand.Read(b); err != nil {
		// crypto/rand failure is fatal for security tokens; panic rather than
		// return a predictable value.
		panic("server: crypto/rand failed: " + err.Error())
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

// hashParams returns a stable hash of normalized params for nonce binding.
func hashParams(params []byte) string {
	sum := sha256.Sum256(params)
	return hex.EncodeToString(sum[:])
}

// ---- confirmation nonces --------------------------------------------------

type nonce struct {
	operation  string
	resource   string
	generation uint64
	session    string
	paramsHash string
	expiresAt  time.Time
}

// nonceStore holds single-use, scoped, expiring confirmation nonces.
type nonceStore struct {
	mu  sync.Mutex
	m   map[string]nonce
	now func() time.Time
}

func newNonceStore(now func() time.Time) *nonceStore {
	return &nonceStore{m: make(map[string]nonce), now: now}
}

// issue creates a nonce bound to the operation, resource, generation, session,
// and params hash, valid for ttl.
func (s *nonceStore) issue(operation, resource string, generation uint64, session, paramsHash string, ttl time.Duration) (string, time.Time) {
	token := randToken(32)
	exp := s.now().Add(ttl)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.gcLocked()
	s.m[token] = nonce{
		operation:  operation,
		resource:   resource,
		generation: generation,
		session:    session,
		paramsHash: paramsHash,
		expiresAt:  exp,
	}
	return token, exp
}

// consume validates and single-use-consumes a nonce. It returns true only if the
// token exists, is unexpired, and matches every bound field exactly.
func (s *nonceStore) consume(token, operation, resource string, generation uint64, session, paramsHash string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	n, ok := s.m[token]
	if !ok {
		return false
	}
	// Remove first: any use (even a mismatched one) burns the nonce.
	delete(s.m, token)
	if s.now().After(n.expiresAt) {
		return false
	}
	if n.operation != operation || n.resource != resource || n.generation != generation {
		return false
	}
	if subtle.ConstantTimeCompare([]byte(n.session), []byte(session)) != 1 {
		return false
	}
	if subtle.ConstantTimeCompare([]byte(n.paramsHash), []byte(paramsHash)) != 1 {
		return false
	}
	return true
}

func (s *nonceStore) gcLocked() {
	now := s.now()
	for k, v := range s.m {
		if now.After(v.expiresAt) {
			delete(s.m, k)
		}
	}
}

// ---- idempotency cache ----------------------------------------------------

type idemEntry struct {
	status    int
	body      []byte
	expiresAt time.Time
	// pending marks an in-flight reservation: a request is currently executing
	// under this key and no response has been cached yet.
	pending bool
	// fingerprint binds this entry to the exact request that created it (its
	// operation, asserted generation, and normalized params). A request id is
	// client-chosen, so without this binding a reused id could replay a response
	// belonging to a different payload — or have a different payload's response
	// cached against it.
	fingerprint string
}

// idemResult is the outcome of a lookup or reservation attempt.
type idemResult int

const (
	// idemReserved: the caller now owns execution and must call complete or
	// release exactly once.
	idemReserved idemResult = iota
	// idemInFlight: another request with the same key is currently executing.
	idemInFlight
	// idemDone: a cached response is available (returned in the entry).
	idemDone
	// idemMismatch: the key is in use by a request with a different
	// operation/params fingerprint. The caller must reject rather than replay.
	idemMismatch
	// idemMiss: no live entry exists for the key (peek only).
	idemMiss
)

// idemStore caches mutation responses by session+request id so a network retry
// cannot repeat a mutation, and reserves in-flight keys so concurrent retries of
// the same request execute exactly once (section 12).
type idemStore struct {
	mu  sync.Mutex
	m   map[string]idemEntry
	now func() time.Time
}

func newIdemStore(now func() time.Time) *idemStore {
	return &idemStore{m: make(map[string]idemEntry), now: now}
}

func idemKey(session, requestID string) string {
	return session + "\x00" + requestID
}

// peek resolves a key without reserving it. It returns idemDone with the cached
// response when one exists for this exact fingerprint, idemMismatch when the key
// belongs to a different request, and idemMiss otherwise (including an in-flight
// reservation of the same request, whose authoritative check is reserve).
func (s *idemStore) peek(key, fingerprint string) (idemEntry, idemResult) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.m[key]
	if !ok {
		return idemEntry{}, idemMiss
	}
	if s.now().After(e.expiresAt) {
		delete(s.m, key)
		return idemEntry{}, idemMiss
	}
	if e.fingerprint != fingerprint {
		return idemEntry{}, idemMismatch
	}
	if e.pending {
		return idemEntry{}, idemMiss
	}
	return e, idemDone
}

// reserve atomically resolves a key: it returns a cached completed response
// (idemDone), reports a concurrent in-flight duplicate (idemInFlight), rejects a
// request id already bound to a different payload (idemMismatch), or marks the
// key reserved for this caller (idemReserved). A reservation expires after
// reservationTTL so a crashed owner cannot block the key forever.
func (s *idemStore) reserve(key, fingerprint string, reservationTTL time.Duration) (idemEntry, idemResult) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.gcLocked()
	if e, ok := s.m[key]; ok {
		if e.fingerprint != fingerprint {
			return idemEntry{}, idemMismatch
		}
		if e.pending {
			return idemEntry{}, idemInFlight
		}
		return e, idemDone
	}
	s.m[key] = idemEntry{pending: true, expiresAt: s.now().Add(reservationTTL), fingerprint: fingerprint}
	return idemEntry{}, idemReserved
}

// complete replaces a reservation with the final cached response, held for ttl.
// The fingerprint is carried over so a later reuse of the request id with a
// different payload is still rejected rather than replayed.
func (s *idemStore) complete(key, fingerprint string, status int, body []byte, ttl time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cp := make([]byte, len(body))
	copy(cp, body)
	s.m[key] = idemEntry{status: status, body: cp, expiresAt: s.now().Add(ttl), fingerprint: fingerprint}
}

// release drops a reservation without caching a result, so a retry may proceed.
// It only removes the entry if it is still this caller's pending reservation.
func (s *idemStore) release(key string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if e, ok := s.m[key]; ok && e.pending {
		delete(s.m, key)
	}
}

func (s *idemStore) gcLocked() {
	now := s.now()
	for k, v := range s.m {
		if now.After(v.expiresAt) {
			delete(s.m, k)
		}
	}
}

// ---- rate limiter ---------------------------------------------------------

type bucket struct {
	tokens float64
	last   time.Time
}

// Rate-limiter idle-bucket eviction bounds: a bucket untouched for rlIdleTTL has
// fully refilled and is indistinguishable from a fresh one, so it can be dropped;
// the sweep runs at most every rlSweepInterval to keep allow() O(1) amortized.
const (
	rlSweepInterval = time.Minute
	rlIdleTTL       = 10 * time.Minute
)

// rateLimiter is a per-key token bucket. every is the interval to accrue one
// token; burst is the bucket capacity.
type rateLimiter struct {
	mu        sync.Mutex
	m         map[string]*bucket
	every     time.Duration
	burst     int
	now       func() time.Time
	lastSweep time.Time
}

func newRateLimiter(every time.Duration, burst int, now func() time.Time) *rateLimiter {
	return &rateLimiter{m: make(map[string]*bucket), every: every, burst: burst, now: now, lastSweep: now()}
}

// allow reports whether a request keyed by key may proceed, consuming a token.
func (r *rateLimiter) allow(key string) bool {
	now := r.now()
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sweepLocked(now)
	b, ok := r.m[key]
	if !ok {
		r.m[key] = &bucket{tokens: float64(r.burst) - 1, last: now}
		return true
	}
	elapsed := now.Sub(b.last)
	b.last = now
	b.tokens += float64(elapsed) / float64(r.every)
	if b.tokens > float64(r.burst) {
		b.tokens = float64(r.burst)
	}
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

// sweepLocked evicts idle buckets so the map cannot grow unbounded over a
// long-lived daemon. The caller holds r.mu.
func (r *rateLimiter) sweepLocked(now time.Time) {
	if now.Sub(r.lastSweep) < rlSweepInterval {
		return
	}
	r.lastSweep = now
	for k, b := range r.m {
		if now.Sub(b.last) >= rlIdleTTL {
			delete(r.m, k)
		}
	}
}
