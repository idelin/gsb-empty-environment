package cacheaudit

import "fmt"

// Kind 是缓存审计事件的类型。
type Kind int

const (
	KindHit     Kind = iota // 命中：返回已发布版本的值
	KindMiss                // 失效：未命中（含未发布加载、已淘汰、加载失败）
	KindLoad                // 加载：从数据源取回某版本
	KindEvict               // 淘汰：移除缓存项
	KindPublish             // 发布：使已加载版本对命中可见
)

func (k Kind) String() string {
	switch k {
	case KindHit:
		return "HIT"
	case KindMiss:
		return "MISS"
	case KindLoad:
		return "LOAD"
	case KindEvict:
		return "EVICT"
	case KindPublish:
		return "PUBLISH"
	}
	return "UNKNOWN"
}

// Event 是一条审计事件。Start/End 为调用-返回区间（来自可注入时钟）。
// Source 记录返回值版本的来源：Hit 为 "load:<seq>"，Load 为 "loader"。
type Event struct {
	Seq     uint64
	Start   int64
	End     int64
	Kind    Kind
	Key     string
	Version uint64
	Source  string
	Err     string
}

func (e Event) String() string {
	s := fmt.Sprintf("#%d [%d,%d] %-7s key=%q v=%d", e.Seq, e.Start, e.End, e.Kind, e.Key, e.Version)
	if e.Source != "" {
		s += " src=" + e.Source
	}
	if e.Err != "" {
		s += " err=" + e.Err
	}
	return s
}

// Log 是有界环形事件日志。容量写满后覆盖最旧事件并累计 dropped，
// dropped>0 表示证据不完整，审计器必须报告证据不足而不是猜测完整历史。
type Log struct {
	buf     []Event
	next    int // 下一个写入位置
	size    int
	nextSeq uint64
	dropped uint64
	now     func() int64
}

// NewLog 创建容量为 capacity 的有界日志；now 为可注入时钟。
func NewLog(capacity int, now func() int64) *Log {
	if capacity < 1 {
		capacity = 1
	}
	return &Log{buf: make([]Event, capacity), nextSeq: 1, now: now}
}

// Append 追加事件并返回其副本（含分配的 Seq）。
func (l *Log) Append(kind Kind, key string, version uint64, source, err string, start, end int64) Event {
	ev := Event{
		Seq:     l.nextSeq,
		Start:   start,
		End:     end,
		Kind:    kind,
		Key:     key,
		Version: version,
		Source:  source,
		Err:     err,
	}
	l.nextSeq++
	if l.size == len(l.buf) {
		l.dropped++
	} else {
		l.size++
	}
	l.buf[l.next] = ev
	l.next = (l.next + 1) % len(l.buf)
	return ev
}

// Events 按 Seq 升序返回现存事件。
func (l *Log) Events() []Event {
	out := make([]Event, 0, l.size)
	for i := 0; i < l.size; i++ {
		idx := (l.next - l.size + i + len(l.buf)) % len(l.buf)
		out = append(out, l.buf[idx])
	}
	return out
}

// Dropped 返回因环形覆盖而丢失的事件数。
func (l *Log) Dropped() uint64 { return l.dropped }

// Complete 报告日志是否证据完整：无覆盖丢失且 Seq 连续无缺口。
func (l *Log) Complete() bool {
	if l.dropped > 0 {
		return false
	}
	evs := l.Events()
	for i, ev := range evs {
		if ev.Seq != uint64(i)+1 {
			return false
		}
	}
	return true
}
