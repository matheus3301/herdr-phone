package auth

import "sync"

// singleflightGroup collapses concurrent calls sharing a key into a single
// execution of fn, giving every caller the same result. It exists so a burst of
// requests carrying an unseen JWT kid triggers exactly one JWKS fetch. This is a
// minimal internal implementation to avoid a dependency on
// golang.org/x/sync/singleflight.
type singleflightGroup struct {
	mu sync.Mutex
	m  map[string]*sfCall
}

type sfCall struct {
	wg  sync.WaitGroup
	val any
	err error
}

// Do executes fn for key, ensuring only one execution is in flight for a given
// key at a time. shared reports whether the result was shared with other
// callers.
func (g *singleflightGroup) Do(key string, fn func() (any, error)) (val any, err error, shared bool) {
	g.mu.Lock()
	if g.m == nil {
		g.m = make(map[string]*sfCall)
	}
	if c, ok := g.m[key]; ok {
		g.mu.Unlock()
		c.wg.Wait()
		return c.val, c.err, true
	}
	c := new(sfCall)
	c.wg.Add(1)
	g.m[key] = c
	g.mu.Unlock()

	c.val, c.err = fn()
	c.wg.Done()

	g.mu.Lock()
	delete(g.m, key)
	g.mu.Unlock()

	return c.val, c.err, false
}
