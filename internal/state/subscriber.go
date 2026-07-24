package state

import "sync"

// Subscription is a per-client outbound queue of snapshots. Consecutive
// snapshots coalesce to the newest, so a slow reader never grows the queue past
// one item and never blocks the engine. The byte bound is the effective hard
// safety net: if even the coalesced latest snapshot cannot fit the client's
// budget, the subscription overflows and is dropped rather than blocking others.
//
// maxItems is retained as a defensive cap but, because snapshots always coalesce
// to a single pending item, it can only ever be reached at maxItems < 1; the
// byte bound is what disciplines a real slow client.
type Subscription struct {
	maxItems int
	maxBytes int

	mu       sync.Mutex
	pending  []*Snapshot
	bytes    int
	closed   bool
	overflow bool

	notify chan struct{}
	done   chan struct{}
}

// newSubscription builds an empty queue. The engine seeds the current snapshot
// through enqueue, so the seed is subject to the same byte/item bounds as any
// broadcast (a seed that exceeds the budget overflows immediately rather than
// silently bypassing the guard).
func newSubscription(maxItems, maxBytes int) *Subscription {
	return &Subscription{
		maxItems: maxItems,
		maxBytes: maxBytes,
		notify:   make(chan struct{}, 1),
		done:     make(chan struct{}),
	}
}

// Notify fires (coalesced) whenever new snapshots are available to drain.
func (s *Subscription) Notify() <-chan struct{} { return s.notify }

// Done is closed when the subscription is closed or overflows.
func (s *Subscription) Done() <-chan struct{} { return s.done }

// Overflow reports whether the subscription was dropped for exceeding its
// bounds.
func (s *Subscription) Overflow() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.overflow
}

// Drain returns and clears the pending snapshots (at most one after
// coalescing). It returns nil when nothing is pending.
func (s *Subscription) Drain() []*Snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.pending) == 0 {
		return nil
	}
	out := s.pending
	s.pending = nil
	s.bytes = 0
	return out
}

// Latest returns the newest pending snapshot without clearing the queue, or nil.
func (s *Subscription) Latest() *Snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.pending) == 0 {
		return nil
	}
	return s.pending[len(s.pending)-1]
}

// enqueue delivers a snapshot, coalescing with any undelivered snapshot. It
// returns false if the subscription overflowed (and is now closed) or was
// already closed.
func (s *Subscription) enqueue(snap *Snapshot) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return false
	}
	// Coalesce: an undelivered older snapshot is worthless once a newer one
	// arrives, so replace it. This keeps depth at one for the steady state.
	s.pending = append(s.pending, snap)
	if len(s.pending) > 1 {
		s.pending = s.pending[len(s.pending)-1:]
	}
	s.bytes = snap.bytes

	// Hard safety bound. With coalescing this can only trip when a single
	// snapshot exceeds the client's byte budget, which means the client cannot
	// be served — drop it.
	if s.bytes > s.maxBytes || len(s.pending) > s.maxItems {
		s.overflow = true
		s.closeLocked()
		return false
	}
	s.signal()
	return true
}

func (s *Subscription) signal() {
	select {
	case s.notify <- struct{}{}:
	default:
	}
}

// Close releases the subscription. Safe to call multiple times.
func (s *Subscription) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closeLocked()
}

func (s *Subscription) closeLocked() {
	if s.closed {
		return
	}
	s.closed = true
	s.pending = nil
	s.bytes = 0
	close(s.done)
}
