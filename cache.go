package netsec

import (
	"container/list"
	"crypto/sha256"
	"time"
)

// defaultCacheTTL NTS 防重放窗口。RFC 8915 要求覆盖正常往返与时钟漂移，
// 10 分钟足以丢弃迟到的旧请求，又不会让缓存无界增长。
const defaultCacheTTL = 10 * time.Minute

// defaultCacheCapacity 每个缓存保留的条目上限，超出时淘汰最久未使用项。
const defaultCacheCapacity = 1 << 16

type cacheEntry struct {
	key    [32]byte
	expiry time.Time
}

// ttlCache 是线程安全（由调用方持锁）的 TTL + LRU 集合，
// 用于 Unique Identifier 重放检测与 Cookie 一次性消费。
type ttlCache struct {
	ttl      time.Duration
	capacity int
	now      func() time.Time
	entries  map[[32]byte]*list.Element
	lru      *list.List // 队首最近使用，队尾最久未使用
}

func newTTLCache(ttl time.Duration, capacity int, now func() time.Time) *ttlCache {
	if ttl <= 0 {
		ttl = defaultCacheTTL
	}
	if capacity <= 0 {
		capacity = defaultCacheCapacity
	}
	if now == nil {
		now = time.Now
	}
	return &ttlCache{
		ttl:      ttl,
		capacity: capacity,
		now:      now,
		entries:  make(map[[32]byte]*list.Element),
		lru:      list.New(),
	}
}

func cacheKey(raw []byte) [32]byte {
	return sha256.Sum256(raw)
}

// seen 判断 key 是否已存在且未过期；过期条目按未命中处理。
func (c *ttlCache) seen(key [32]byte) bool {
	elem, ok := c.entries[key]
	if !ok {
		return false
	}
	entry := elem.Value.(*cacheEntry)
	if c.now().After(entry.expiry) {
		c.lru.Remove(elem)
		delete(c.entries, key)
		return false
	}
	c.lru.MoveToFront(elem)
	return true
}

// add 插入或刷新一个 key，必要时淘汰过期项或 LRU 尾部。
func (c *ttlCache) add(key [32]byte) {
	now := c.now()
	if elem, ok := c.entries[key]; ok {
		elem.Value.(*cacheEntry).expiry = now.Add(c.ttl)
		c.lru.MoveToFront(elem)
		return
	}
	if len(c.entries) >= c.capacity {
		c.evict(now)
	}
	elem := c.lru.PushFront(&cacheEntry{
		key:    key,
		expiry: now.Add(c.ttl),
	})
	c.entries[key] = elem
}

// evict 先清理过期项；容量仍满时淘汰 LRU 链表尾部。
func (c *ttlCache) evict(now time.Time) {
	for elem := c.lru.Back(); elem != nil; {
		entry := elem.Value.(*cacheEntry)
		if !now.After(entry.expiry) {
			break
		}
		prev := elem.Prev()
		c.lru.Remove(elem)
		delete(c.entries, entry.key)
		elem = prev
	}
	if len(c.entries) >= c.capacity {
		elem := c.lru.Back()
		entry := elem.Value.(*cacheEntry)
		c.lru.Remove(elem)
		delete(c.entries, entry.key)
	}
}
