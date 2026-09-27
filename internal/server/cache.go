package server

import (
	"sync"
	"time"
)

// countCache memoizes index document counts for a short TTL.
type countCache struct {
	mu  sync.Mutex
	ttl time.Duration
	m   map[string]countEntry
}

type countEntry struct {
	v   int64
	exp time.Time
}

func newCountCache(ttl time.Duration) *countCache {
	return &countCache{ttl: ttl, m: map[string]countEntry{}}
}

func (c *countCache) get(k string) (int64, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.m[k]
	if !ok || time.Now().After(e.exp) {
		return 0, false
	}
	return e.v, true
}

func (c *countCache) set(k string, v int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.m[k] = countEntry{v: v, exp: time.Now().Add(c.ttl)}
}
