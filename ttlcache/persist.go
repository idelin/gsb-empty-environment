package ttlcache

import (
	"time"
)

// Record 持久化记录：键、值、保存时刻的剩余寿命，以及保存时刻本身。
// 只存剩余寿命与保存时刻，不存绝对到期时间，恢复方据此扣减停机时长。
type Record[V any] struct {
	Key       string
	Value     V
	Remaining time.Duration
	SavedAt   time.Duration
}

// Snapshot 导出当前所有未过期键的持久化记录。
// Remaining 按拍摄快照时刻的单调时钟计算。
func (c *Cache[V]) Snapshot() []Record[V] {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.clock.Now()
	records := make([]Record[V], 0, len(c.items))
	for key, e := range c.items {
		remaining := e.expiresAt - now
		if remaining <= 0 {
			continue
		}
		records = append(records, Record[V]{
			Key:       key,
			Value:     e.value,
			Remaining: remaining,
			SavedAt:   now,
		})
	}
	return records
}

// Restore 用持久化记录与停机时长重建缓存。
// 每条记录的有效剩余寿命 = Remaining - downtime：
//   - 结果非正：该键在停机期间已死，立即过期，不写入缓存；
//   - 结果为正：以剩余寿命续命，绝不重新给满 TTL。
//
// 恢复出的键一律分配新代，与停机前遗留的任何事件无关。
// 返回恢复时即被判为已过期的记录数。
func Restore[V any](clock Clock, records []Record[V], downtime time.Duration) (*Cache[V], int) {
	c := New[V](clock)
	now := clock.Now()
	expired := 0
	for _, r := range records {
		remaining := r.Remaining - downtime
		if remaining <= 0 {
			expired++
			continue
		}
		c.mu.Lock()
		c.nextGen++
		gen := c.nextGen
		c.items[r.Key] = entry[V]{value: r.Value, expiresAt: now + remaining, generation: gen}
		c.pushEventLocked(r.Key, gen, now+remaining)
		c.mu.Unlock()
	}
	return c, expired
}
