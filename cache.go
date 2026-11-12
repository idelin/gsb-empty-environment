package netsec

import (
	"sync"
	"time"
)

// Clock 提供当前时间，便于在测试中注入假时钟。
type Clock interface {
	Now() time.Time
}

// ClockFunc 把函数适配为 Clock。
type ClockFunc func() time.Time

// Now 实现 Clock。
func (f ClockFunc) Now() time.Time { return f() }

type realClock struct{}

func (realClock) Now() time.Time { return time.Now() }

// Cache 是按整组存储资源记录集合的并发安全缓存。
//
// 集合内的记录寿命可能不一致，安全解析器不能拆出与签名不一致的子集，
// 因此整组以最小 TTL 到期，更新时原子替换整组并保留验证级别。
type Cache struct {
	mu     sync.RWMutex
	clock  Clock
	groups map[Key]*group
}

// NewCache 创建缓存；clock 为 nil 时使用真实时钟。
func NewCache(clock Clock) *Cache {
	if clock == nil {
		clock = realClock{}
	}
	return &Cache{clock: clock, groups: make(map[Key]*group)}
}

// Put 以整组方式写入集合，原子替换同键的旧组并记录验证级别。
//
// 集合内完全重复的记录会被去重；整组到期时间取最小 TTL。
// 若同键旧组仍未到期且信任级别更高，则拒绝低信任的覆盖，
// 返回 ErrTrustDowngrade，旧组及其验证级别保持不变。
func (c *Cache) Put(records []Record, trust TrustLevel) (Key, error) {
	key, deduped, minTTL, err := normalize(records)
	if err != nil {
		return Key{}, err
	}
	now := c.clock.Now()
	g := &group{
		records:   deduped,
		trust:     trust,
		expiresAt: now.Add(time.Duration(minTTL) * time.Second),
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if old, ok := c.groups[key]; ok && !old.expired(now) && old.trust > trust {
		return key, ErrTrustDowngrade
	}
	c.groups[key] = g
	return key, nil
}

// Get 按键查询整组。组不存在或已到期时返回 ok=false；
// 到期的组会被顺手清除。返回的记录是副本，调用方可安全修改。
func (c *Cache) Get(key Key) (records []Record, trust TrustLevel, ok bool) {
	now := c.clock.Now()
	c.mu.RLock()
	g, found := c.groups[key]
	if !found || g.expired(now) {
		c.mu.RUnlock()
		if found {
			c.evict(key, g)
		}
		return nil, TrustNone, false
	}
	out := make([]Record, len(g.records))
	copy(out, g.records)
	trust = g.trust
	c.mu.RUnlock()
	return out, trust, true
}

// evict 仅在组未被并发更新替换过时删除它。
func (c *Cache) evict(key Key, want *group) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if cur, ok := c.groups[key]; ok && cur == want {
		delete(c.groups, key)
	}
}

// Len 返回当前缓存的组数（含尚未被清除的到期组）。
func (c *Cache) Len() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.groups)
}
