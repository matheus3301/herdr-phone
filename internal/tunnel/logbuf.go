package tunnel

import "sync"

// ringBuffer keeps the most recent sanitized log lines with a fixed capacity so
// diagnostics stay bounded regardless of how noisy cloudflared is.
type ringBuffer struct {
	mu    sync.Mutex
	lines []string
	cap   int
}

func newRingBuffer(capacity int) *ringBuffer {
	if capacity <= 0 {
		capacity = 200
	}
	return &ringBuffer{cap: capacity}
}

func (r *ringBuffer) add(line string) {
	if line == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.lines) >= r.cap {
		// Drop the oldest line. Copying keeps the slice from growing unbounded.
		copy(r.lines, r.lines[1:])
		r.lines[len(r.lines)-1] = line
		return
	}
	r.lines = append(r.lines, line)
}

func (r *ringBuffer) snapshot() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, len(r.lines))
	copy(out, r.lines)
	return out
}
