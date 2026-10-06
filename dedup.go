// Package dedup 提供基于事件 ID 的去重。
//
// 语义:
//   - 同一事件 ID 只保留首次有效载荷,后续重复事件返回首次载荷并标记冲突。
//   - 每个来源通过 ReportProgress 显式上报进度(单调递增),全局水位为所有
//     已上报来源进度最小值的单调推进,只增不减。
//   - 仅当水位超过某条目的 事件时间+保留期 时,该条目才被回收。
//   - 重放一个已被回收的 ID 时,库内已无记录,此时依据该事件来源的显式进度
//     判定其过旧(Stale),绝不能当作新事件再次接受。
package dedup

// Outcome 表示一次 Ingest 的判定结果。
type Outcome int

const (
	// Accepted 表示首次出现的 ID,有效载荷已被保留。
	Accepted Outcome = iota
	// Duplicate 表示 ID 已知,保留的仍是首次有效载荷。
	Duplicate
	// Stale 表示 ID 不在库中且事件不新于其来源的显式进度,
	// 属于已回收 ID 的重放或迟到事件,不得当作新事件。
	Stale
)

func (o Outcome) String() string {
	switch o {
	case Accepted:
		return "Accepted"
	case Duplicate:
		return "Duplicate"
	case Stale:
		return "Stale"
	}
	return "Unknown"
}

// Event 是一条待去重的事件。EventTime 为事件时间(逻辑时间戳)。
type Event struct {
	Source    string
	ID        string
	EventTime int64
	Payload   string
}

// Result 是 Ingest 的返回值。
type Result struct {
	Outcome Outcome
	// Payload 在 Outcome 为 Accepted 或 Duplicate 时是保留的首次有效载荷。
	Payload string
	// Conflict 为 true 表示本次载荷与已保留的首次载荷不一致(仅 Duplicate)。
	Conflict bool
}

type entry struct {
	payload   string
	eventTime int64
}

// Deduper 按事件 ID 去重,并按水位加保留期回收旧条目。
// 不是并发安全的;如需并发使用请自行加锁。
type Deduper struct {
	retention int64
	progress  map[string]int64 // 各来源的显式进度,单调不减
	watermark int64
	hasWM     bool
	entries   map[string]entry
}

// New 创建一个 Deduper,retention 为事件时间之后的保留期。
// 条目仅在水位 > 事件时间+retention 时被回收。
func New(retention int64) *Deduper {
	return &Deduper{
		retention: retention,
		progress:  make(map[string]int64),
		entries:   make(map[string]entry),
	}
}

// ReportProgress 上报某来源的显式进度。进度单调不减,回退的上报被忽略。
// 上报后重新计算水位(所有已上报来源进度的最小值)并回收过期条目。
// 返回当前水位;第二个返回值表示水位是否已存在(至少一个来源上报过)。
func (d *Deduper) ReportProgress(source string, t int64) (int64, bool) {
	if cur, ok := d.progress[source]; ok && t <= cur {
		return d.watermark, d.hasWM
	}
	d.progress[source] = t
	d.advanceWatermark()
	return d.watermark, d.hasWM
}

// Watermark 返回当前水位;第二个返回值表示水位是否已存在。
func (d *Deduper) Watermark() (int64, bool) {
	return d.watermark, d.hasWM
}

func (d *Deduper) advanceWatermark() {
	first := true
	var wm int64
	for _, p := range d.progress {
		if first || p < wm {
			wm = p
			first = false
		}
	}
	if first {
		return
	}
	if !d.hasWM || wm > d.watermark {
		d.watermark = wm
		d.hasWM = true
	}
	d.reclaim()
}

// reclaim 回收所有满足 水位 > 事件时间+保留期 的条目。
func (d *Deduper) reclaim() {
	if !d.hasWM {
		return
	}
	for id, e := range d.entries {
		if d.watermark > e.eventTime+d.retention {
			delete(d.entries, id)
		}
	}
}

// Ingest 判定一条事件。
//
// 判定顺序:
//  1. ID 在库中:返回 Duplicate 与首次有效载荷,载荷不一致时标记 Conflict。
//  2. ID 不在库中,但事件时间不新于其来源的显式进度:返回 Stale。
//     这覆盖了已回收 ID 的重放,以及迟到的未知事件。
//  3. 否则接受为新事件,保留其载荷。
func (d *Deduper) Ingest(ev Event) Result {
	if e, ok := d.entries[ev.ID]; ok {
		return Result{
			Outcome:  Duplicate,
			Payload:  e.payload,
			Conflict: e.payload != ev.Payload,
		}
	}
	if p, ok := d.progress[ev.Source]; ok && ev.EventTime <= p {
		return Result{Outcome: Stale}
	}
	d.entries[ev.ID] = entry{payload: ev.Payload, eventTime: ev.EventTime}
	return Result{Outcome: Accepted, Payload: ev.Payload}
}

// Lookup 返回库中某 ID 保留的首次有效载荷与事件时间。
func (d *Deduper) Lookup(id string) (payload string, eventTime int64, ok bool) {
	e, ok := d.entries[id]
	return e.payload, e.eventTime, ok
}

// Len 返回当前库中保留的条目数。
func (d *Deduper) Len() int {
	return len(d.entries)
}
