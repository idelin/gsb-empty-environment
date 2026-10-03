package schema

import (
	"reflect"
	"sync"
)

// cache memoizes validated schemas. RWMutex-guarded; no unbounded growth
// concerns for realistic type sets, and no goroutine or resource leaks.
type cache struct {
	mu sync.RWMutex
	m  map[reflect.Type]*MessageInfo
}

func newCache() *cache { return &cache{m: make(map[reflect.Type]*MessageInfo)} }

func (c *cache) get(t reflect.Type) (*MessageInfo, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	info, ok := c.m[t]
	return info, ok
}

func (c *cache) put(t reflect.Type, info *MessageInfo) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.m[t] = info
}
