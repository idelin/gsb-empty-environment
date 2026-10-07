package groupx

import "container/list"

// lru is a small per-tenant LRU cache. Each tenant owns a private instance;
// entries are never shared or looked up across tenants.
type lru struct {
	cap   int
	items map[string]*list.Element
	order *list.List // front = most recently used
}

type lruEntry struct {
	key   string
	value string
}

func newLRU(capacity int) *lru {
	if capacity < 1 {
		capacity = 1
	}
	return &lru{cap: capacity, items: make(map[string]*list.Element), order: list.New()}
}

func (c *lru) put(key, value string) {
	if el, ok := c.items[key]; ok {
		el.Value.(*lruEntry).value = value
		c.order.MoveToFront(el)
		return
	}
	el := c.order.PushFront(&lruEntry{key: key, value: value})
	c.items[key] = el
	if c.order.Len() > c.cap {
		back := c.order.Back()
		if back != nil {
			c.order.Remove(back)
			delete(c.items, back.Value.(*lruEntry).key)
		}
	}
}

func (c *lru) get(key string) (string, bool) {
	el, ok := c.items[key]
	if !ok {
		return "", false
	}
	c.order.MoveToFront(el)
	return el.Value.(*lruEntry).value, true
}

func (c *lru) len() int { return c.order.Len() }

// clone deep-copies the cache, preserving eviction order.
func (c *lru) clone() *lru {
	out := newLRU(c.cap)
	for el := c.order.Back(); el != nil; el = el.Prev() {
		e := el.Value.(*lruEntry)
		out.put(e.key, e.value)
	}
	return out
}
