package lsmwatch

// Engine 是一个简化的 LSM 存储引擎：MemTable + 多层 SSTable（新表在前）。
// 每次 Get 都会向 Monitor 上报本次实际读取的 SSTable 数量与 Bloom 误判次数。
type Engine struct {
	mem            map[string]string
	tables         []*SSTable // 新 -> 旧
	nextID         int
	monitor        *Monitor
	BitsPerKey     int // 新建/重建 Bloom Filter 时使用的位数
	FlushThreshold int // MemTable 达到该条数自动 Flush
}

// NewEngine 创建引擎，monitor 可为 nil（不统计）。
func NewEngine(monitor *Monitor) *Engine {
	return &Engine{
		mem:            make(map[string]string),
		monitor:        monitor,
		BitsPerKey:     8,
		FlushThreshold: 1 << 30, // 默认不自动 Flush，由调用方控制
	}
}

// Put 写入 MemTable，达到阈值自动 Flush。
func (e *Engine) Put(key, value string) {
	e.mem[key] = value
	if len(e.mem) >= e.FlushThreshold {
		e.Flush()
	}
}

// Flush 将 MemTable 固化为一个新的 SSTable（置于最新位置）。
func (e *Engine) Flush() {
	if len(e.mem) == 0 {
		return
	}
	t := NewSSTable(e.nextID, e.mem, e.BitsPerKey)
	e.nextID++
	e.tables = append([]*SSTable{t}, e.tables...)
	e.mem = make(map[string]string)
}

// Get 查询 key：先查 MemTable，再按新到旧扫描 SSTable。
// Bloom Filter 判定"不存在"的表会被跳过（不产生磁盘读）；
// 判定"可能存在"或过滤器被关闭的表会被真实读取（计入读放大）。
func (e *Engine) Get(key string) (string, bool) {
	if v, ok := e.mem[key]; ok {
		e.record(GetSample{})
		return v, true
	}
	var sample GetSample
	for _, t := range e.tables {
		if t.filterActive() {
			sample.BloomChecks++
			if !t.mayContain(key) {
				continue
			}
		}
		sample.Reads++
		if v, ok := t.get(key); ok {
			e.record(sample)
			return v, true
		}
		if t.filterActive() {
			sample.FalsePositives++ // Bloom 说"可能有"，实际没有
		}
	}
	e.record(sample)
	return "", false
}

// Compact 全量合并所有 SSTable 为一个（新值覆盖旧值），是压制读放大的手段。
func (e *Engine) Compact() {
	if len(e.tables) == 0 {
		return
	}
	merged := make(map[string]string)
	for i := len(e.tables) - 1; i >= 0; i-- { // 旧 -> 新，新值覆盖旧值
		for k, v := range e.tables[i].entries {
			merged[k] = v
		}
	}
	t := NewSSTable(e.nextID, merged, e.BitsPerKey)
	e.nextID++
	e.tables = []*SSTable{t}
}

// SetBloomEnabled 打开/关闭所有 SSTable 的 Bloom Filter（用于故障注入）。
func (e *Engine) SetBloomEnabled(enabled bool) {
	for _, t := range e.tables {
		t.BloomEnabled = enabled
	}
}

// RebuildFilters 以指定 bitsPerKey 重建所有表的 Bloom Filter。
func (e *Engine) RebuildFilters(bitsPerKey int) {
	for _, t := range e.tables {
		t.RebuildFilter(bitsPerKey)
	}
}

// Tables 返回当前 SSTable 列表（新 -> 旧）。
func (e *Engine) Tables() []*SSTable { return e.tables }

func (e *Engine) record(s GetSample) {
	if e.monitor != nil {
		e.monitor.RecordGet(s)
	}
}
