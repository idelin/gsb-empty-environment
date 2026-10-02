package lsmguard

import (
	"sort"
	"sync"
)

// SSTable 模拟一个落盘的有序字符串表。
type SSTable struct {
	id      int
	kv      map[string]string
	bloom   *BloomFilter
	bloomOn bool // false 表示该表的 Bloom Filter 被关闭（故障/配置）
}

// GetStats 是单次 Get 的度量样本，上报给监控器。
type GetStats struct {
	Key            string
	Found          bool
	SSTablesRead   int // 实际发生磁盘读取的 SSTable 数量（读放大分子）
	SSTablesProbed int // 被检查的 SSTable 总数
	BloomPositives int // Bloom 返回 "可能存在" 的次数
	FalsePositives int // Bloom 说存在但实际不存在的次数
}

// Engine 是一个简化的 LSM 存储引擎：写先进 MemTable，满了 Flush 成 SSTable。
type Engine struct {
	mu        sync.Mutex
	memtable  map[string]string
	flushSize int
	tables    []*SSTable // 新表在尾部
	nextID    int

	// 每次 Get 的样本回调（监控器挂接点）
	onGet func(GetStats)
}

func NewEngine(flushSize int) *Engine {
	return &Engine{
		memtable:  make(map[string]string),
		flushSize: flushSize,
	}
}

// SetOnGet 注册度量采样回调。
func (e *Engine) SetOnGet(fn func(GetStats)) { e.onGet = fn }

// Put 写入一个键值对。
func (e *Engine) Put(key, value string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.memtable[key] = value
	if len(e.memtable) >= e.flushSize {
		e.flushLocked()
	}
}

func (e *Engine) flushLocked() {
	if len(e.memtable) == 0 {
		return
	}
	t := &SSTable{
		id:      e.nextID,
		kv:      e.memtable,
		bloom:   NewBloomFilter(uint64(len(e.memtable)), 0.01),
		bloomOn: true,
	}
	e.nextID++
	for k := range t.kv {
		t.bloom.Add([]byte(k))
	}
	e.tables = append(e.tables, t)
	e.memtable = make(map[string]string)
}

// Flush 强制把 MemTable 落盘。
func (e *Engine) Flush() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.flushLocked()
}

// DisableBloom 关闭第 i 个 SSTable 的 Bloom Filter（模拟故障场景）。
func (e *Engine) DisableBloom(i int) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if i >= 0 && i < len(e.tables) {
		e.tables[i].bloomOn = false
	}
}

// NumSSTables 返回当前 SSTable 数量。
func (e *Engine) NumSSTables() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return len(e.tables)
}

// Get 查询 key：先查 MemTable，再从新到旧查 SSTable。
// 有 Bloom Filter 的表先问过滤器，否定则跳过（不产生磁盘读）。
func (e *Engine) Get(key string) (string, bool) {
	e.mu.Lock()
	if v, ok := e.memtable[key]; ok {
		e.mu.Unlock()
		return v, true
	}
	// 拷贝表快照，避免持锁做 IO 模拟
	tables := make([]*SSTable, len(e.tables))
	copy(tables, e.tables)
	onGet := e.onGet
	e.mu.Unlock()

	stats := GetStats{Key: key}
	var val string
	found := false
	for i := len(tables) - 1; i >= 0; i-- {
		t := tables[i]
		stats.SSTablesProbed++
		if t.bloomOn {
			if !t.bloom.MaybePresent([]byte(key)) {
				continue // 过滤器拦截，无磁盘读
			}
			stats.BloomPositives++
		}
		// 发生一次 "磁盘读取"
		stats.SSTablesRead++
		v, ok := t.kv[key]
		if t.bloomOn {
			t.bloom.RecordResult(ok)
			if !ok {
				stats.FalsePositives++
			}
		}
		if ok {
			val, found = v, true
			break
		}
	}
	stats.Found = found
	if onGet != nil {
		onGet(stats)
	}
	return val, found
}

// Keys 返回所有已落盘 key 的排序列表（测试/演示用）。
func (e *Engine) Keys() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	var out []string
	for _, t := range e.tables {
		for k := range t.kv {
			out = append(out, k)
		}
	}
	for k := range e.memtable {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
