package cachebench

import (
	"container/list"
	"runtime"
	"sync"
	"sync/atomic"
	"time"
)

const (
	// shardCount 分片数，降低锁竞争；指标采集本身无锁。
	shardCount = 256
	// entryOverheadBytes 每个条目除逻辑数据外的估计物理开销
	// （map 槽位 + LRU 链表节点 + string/slice 头），用于估算碎片率。
	entryOverheadBytes = 96
)

type entry struct {
	key   string
	value []byte
}

type shard struct {
	mu    sync.Mutex
	items map[string]*list.Element
	lru   *list.List
}

// Cache 是分片 LRU 缓存，内置高精度打点，所有指标经 Metrics 无锁采集。
type Cache struct {
	shards      [shardCount]*shard
	capPerShard int
	metrics     *Metrics

	liveEntries  int64
	logicalBytes int64
}

// NewCache 创建容量为 capacityEntries 个条目的缓存，指标写入 metrics。
func NewCache(capacityEntries int, metrics *Metrics) *Cache {
	if capacityEntries < shardCount {
		capacityEntries = shardCount
	}
	c := &Cache{capPerShard: capacityEntries / shardCount, metrics: metrics}
	for i := range c.shards {
		c.shards[i] = &shard{items: make(map[string]*list.Element), lru: list.New()}
	}
	return c
}

func fnv32a(s string) uint32 {
	var h uint32 = 2166136261
	for i := 0; i < len(s); i++ {
		h ^= uint32(s[i])
		h *= 16777619
	}
	return h
}

// Get 读取缓存，记录命中/未命中与微秒级耗时。
func (c *Cache) Get(key string) ([]byte, bool) {
	start := time.Now()
	sh := c.shards[fnv32a(key)&(shardCount-1)]
	sh.mu.Lock()
	el, ok := sh.items[key]
	var val []byte
	if ok {
		sh.lru.MoveToFront(el)
		val = el.Value.(*entry).value
	}
	sh.mu.Unlock()
	c.metrics.RecordGet(ok, time.Since(start))
	if !ok {
		return nil, false
	}
	return val, true
}

// Set 写入缓存，必要时按 LRU 驱逐，记录耗时与驱逐数。
func (c *Cache) Set(key string, value []byte) {
	start := time.Now()
	sh := c.shards[fnv32a(key)&(shardCount-1)]
	var evicted, evictedBytes int64
	sh.mu.Lock()
	if el, ok := sh.items[key]; ok {
		e := el.Value.(*entry)
		atomic.AddInt64(&c.logicalBytes, int64(len(value)-len(e.value)))
		e.value = value
		sh.lru.MoveToFront(el)
	} else {
		el := sh.lru.PushFront(&entry{key: key, value: value})
		sh.items[key] = el
		atomic.AddInt64(&c.liveEntries, 1)
		atomic.AddInt64(&c.logicalBytes, int64(len(key)+len(value)))
		for sh.lru.Len() > c.capPerShard {
			back := sh.lru.Back()
			if back == nil {
				break
			}
			e := back.Value.(*entry)
			delete(sh.items, e.key)
			sh.lru.Remove(back)
			evicted++
			evictedBytes += int64(len(e.key) + len(e.value))
		}
	}
	sh.mu.Unlock()
	if evicted > 0 {
		atomic.AddInt64(&c.liveEntries, -evicted)
		atomic.AddInt64(&c.logicalBytes, -evictedBytes)
	}
	c.metrics.RecordSet(time.Since(start), uint64(evicted))
}

// Metrics 返回缓存绑定的度量组件。
func (c *Cache) Metrics() *Metrics { return c.metrics }

// MemoryStats 汇总缓存级与 Go 堆级内存碎片率。
func (c *Cache) MemoryStats() MemoryReport {
	entries := atomic.LoadInt64(&c.liveEntries)
	logical := atomic.LoadInt64(&c.logicalBytes)
	footprint := logical + entries*entryOverheadBytes
	var cacheFrag float64
	if footprint > 0 {
		cacheFrag = 1 - float64(logical)/float64(footprint)
	}
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	var heapFrag float64
	if ms.HeapInuse > 0 {
		heapFrag = float64(ms.HeapInuse-ms.HeapAlloc) / float64(ms.HeapInuse)
	}
	return MemoryReport{
		LiveEntries:             entries,
		LogicalBytes:            logical,
		EstimatedFootprintBytes: footprint,
		CacheFragmentationRate:  cacheFrag,
		GoHeapAllocBytes:        ms.HeapAlloc,
		GoHeapInuseBytes:        ms.HeapInuse,
		GoHeapFragmentationRate: heapFrag,
	}
}
