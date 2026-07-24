package state

import "testing"

func snap(seq uint64, bytes int) *Snapshot {
	return &Snapshot{Seq: seq, bytes: bytes}
}

func TestSubscriptionCoalescesToNewest(t *testing.T) {
	t.Parallel()
	s := newSubscription(8, 1<<20)
	for i := uint64(1); i <= 5; i++ {
		if !s.enqueue(snap(i, 100)) {
			t.Fatalf("enqueue %d failed", i)
		}
	}
	got := s.Drain()
	if len(got) != 1 {
		t.Fatalf("expected 1 coalesced snapshot, got %d", len(got))
	}
	if got[0].Seq != 5 {
		t.Fatalf("coalesced to seq %d, want newest 5", got[0].Seq)
	}
	// After draining, the queue is empty.
	if s.Drain() != nil {
		t.Fatal("queue should be empty after drain")
	}
}

func TestSubscriptionSeed(t *testing.T) {
	t.Parallel()
	s := newSubscription(8, 1<<20)
	s.enqueue(snap(7, 50))
	select {
	case <-s.Notify():
	default:
		t.Fatal("seed should signal")
	}
	if got := s.Latest(); got == nil || got.Seq != 7 {
		t.Fatalf("seed not present: %+v", got)
	}
}

func TestSubscriptionByteOverflowCloses(t *testing.T) {
	t.Parallel()
	s := newSubscription(8, 64)
	if s.enqueue(snap(1, 65)) {
		t.Fatal("enqueue exceeding byte bound must return false")
	}
	if !s.Overflow() {
		t.Fatal("overflow flag not set")
	}
	select {
	case <-s.Done():
	default:
		t.Fatal("overflow must close the subscription")
	}
	// Further enqueues are rejected.
	if s.enqueue(snap(2, 1)) {
		t.Fatal("closed subscription must reject enqueue")
	}
}

func TestSubscriptionCloseIsIdempotent(t *testing.T) {
	t.Parallel()
	s := newSubscription(8, 1<<20)
	s.Close()
	s.Close() // must not panic on double close
	if s.enqueue(snap(1, 1)) {
		t.Fatal("closed subscription must reject enqueue")
	}
}

func TestSubscriptionNotifyIsCoalesced(t *testing.T) {
	t.Parallel()
	s := newSubscription(8, 1<<20)
	for i := uint64(1); i <= 3; i++ {
		s.enqueue(snap(i, 10))
	}
	// Exactly one pending signal despite multiple enqueues.
	<-s.Notify()
	select {
	case <-s.Notify():
		t.Fatal("notify should coalesce to a single pending signal")
	default:
	}
}
