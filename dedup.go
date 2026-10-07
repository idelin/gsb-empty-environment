// Package dedup 提供按事件 ID 去重的内存去重器。
//
// 语义约定:
//   - 同一 ID 首次出现时保留其载荷, 之后同 ID 事件一律判定为重复,
//     首次载荷不被覆盖(含事件时间不同的冲突场景)。
//   - 每个来源通过 Advance 显式上报进度(该来源不再发出事件时间 <= t 的事件)。
//     去重器自身绝不根据观察到的事件隐式推进来源进度。
//   - 全局水位 = 所有已上报来源进度的最小值, 且单调不减: 新来源以较低
//     进度首次上报不会拉低水位(否则已回收 id 的重放可能被误判为新事件)。
//     没有任何来源上报时水位无效, 不做任何回收。
//   - 仅当水位严格超过 事件时间+保留期 时, 该 ID 的记录才被回收。
//   - 记录被回收后, 同 ID 的重放事件不能当作新事件: 若其事件时间 <= 来源
//     已上报进度, 或 事件时间+保留期 严格小于当前水位, 判定为 Stale。
package dedup

import "sync"

// Verdict 是 Observe 对一次事件的判定结果。
type Verdict int

const (
	// New 表示该 ID 首次出现, 载荷已被保存。
	New Verdict = iota
	// Duplicate 表示该 ID 已有存活记录, 首次载荷保持不变。
	Duplicate
	// Stale 表示该 ID 无存活记录, 但依据显式来源进度/水位可判定
	// 它是已被回收记录的重放(或过旧事件), 不会被当作新事件保存。
	Stale
)

func (v Verdict) String() string {
	switch v {
	case New:
		return "New"
	case Duplicate:
		return "Duplicate"
	case Stale:
		return "Stale"
	}
	return "Unknown"
}

// Event 是一条待去重的事件。Time 为事件时间(逻辑时钟或 Unix 纳秒均可,
// 只需与 Advance 上报的进度使用同一时钟域)。
type Event struct {
	Source  string
	ID      string
	Time    int64
	Payload string
}

type record struct {
	payload string
	time    int64 // 首次出现的事件时间, 回收判定只用它
}

// Deduper 是并发安全的按 ID 去重器。
type Deduper struct {
	mu        sync.Mutex
	retention int64
	records   map[string]record
	progress  map[string]int64 // 各来源显式上报的进度
	watermark int64            // 缓存的水位, 无来源上报时无效
	hasWM     bool
	reclaimed int64 // 累计回收条数(观测用)
}

// NewDeduper 创建保留期为 retention(与 Event.Time 同单位)的去重器。
// retention 必须非负。
func NewDeduper(retention int64) *Deduper {
	if retention < 0 {
		panic("dedup: negative retention")
	}
	return &Deduper{
		retention: retention,
		records:   make(map[string]record),
		progress:  make(map[string]int64),
	}
}

// Observe 判定一条事件。返回值: 判定结果, 以及该 ID 的权威载荷
// (New 时为本次载荷, Duplicate 时为首次载荷, Stale 时为空)。
// 首次载荷一旦写入永不覆盖; Stale 事件不会留下任何记录。
func (d *Deduper) Observe(e Event) (Verdict, string) {
	d.mu.Lock()
	defer d.mu.Unlock()

	if rec, ok := d.records[e.ID]; ok {
		return Duplicate, rec.payload
	}
	// 无存活记录: 判断是重放/过旧, 还是真正的新事件。
	if prog, ok := d.progress[e.Source]; ok && e.Time <= prog {
		return Stale, ""
	}
	if d.hasWM && e.Time+d.retention < d.watermark {
		return Stale, ""
	}
	d.records[e.ID] = record{payload: e.Payload, time: e.Time}
	return New, e.Payload
}

// Advance 显式上报来源进度: 该来源承诺不再发出事件时间 <= t 的事件。
// 进度单调不减, 倒退的上报被忽略。上报后按新水位回收过期记录。
func (d *Deduper) Advance(source string, t int64) {
	d.mu.Lock()
	defer d.mu.Unlock()

	if prog, ok := d.progress[source]; ok && t <= prog {
		return
	}
	d.progress[source] = t
	d.recomputeWatermarkLocked()
	d.reclaimLocked()
}

// Watermark 返回当前全局水位; ok 为 false 表示尚无来源上报进度。
func (d *Deduper) Watermark() (wm int64, ok bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.watermark, d.hasWM
}

// SourceProgress 返回某来源已上报的进度。
func (d *Deduper) SourceProgress(source string) (int64, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	p, ok := d.progress[source]
	return p, ok
}

// Len 返回当前存活(未回收)的记录数。
func (d *Deduper) Len() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.records)
}

// Reclaimed 返回累计被回收的记录数。
func (d *Deduper) Reclaimed() int64 {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.reclaimed
}

// Has 报告某 ID 是否存在存活记录(主要用于测试与调试)。
func (d *Deduper) Has(id string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	_, ok := d.records[id]
	return ok
}

func (d *Deduper) recomputeWatermarkLocked() {
	if len(d.progress) == 0 {
		return
	}
	min := int64(0)
	first := true
	for _, p := range d.progress {
		if first || p < min {
			min = p
			first = false
		}
	}
	if !d.hasWM || min > d.watermark {
		d.watermark = min
	}
	d.hasWM = true
}

// reclaimLocked 仅回收 事件时间+保留期 严格小于水位的记录。
func (d *Deduper) reclaimLocked() {
	if !d.hasWM {
		return
	}
	for id, rec := range d.records {
		if rec.time+d.retention < d.watermark {
			delete(d.records, id)
			d.reclaimed++
		}
	}
}
