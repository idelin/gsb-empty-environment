package correlator

import (
	"bytes"
	"container/list"
	"encoding/gob"
	"fmt"
	"time"
)

// EventKind 是关联器输出的事件类型。
type EventKind int

const (
	// EventMatched 响应在超时前匹配到请求，Latency 有效。
	EventMatched EventKind = iota
	// EventLate 响应到达时请求已超时，记为迟到而非新成功。
	EventLate
	// EventOrphan 响应找不到对应请求（含旧 epoch 迟到响应）。
	EventOrphan
	// EventDuplicate 重放的重复请求，不刷新原始开始时间。
	EventDuplicate
	// EventTimeout 请求挂起超过超时阈值。
	EventTimeout
	// EventEvicted 存储达到上限，最旧的挂起请求被驱逐。
	EventEvicted
)

func (k EventKind) String() string {
	switch k {
	case EventMatched:
		return "MATCHED"
	case EventLate:
		return "LATE"
	case EventOrphan:
		return "ORPHAN"
	case EventDuplicate:
		return "DUPLICATE"
	case EventTimeout:
		return "TIMEOUT"
	case EventEvicted:
		return "EVICTED"
	}
	return "UNKNOWN"
}

// Event 是关联器产生的一条输出。
type Event struct {
	Kind    EventKind
	Epoch   uint64
	Seq     uint64
	Latency time.Duration // 仅 MATCHED / LATE 有效
}

func (e Event) String() string {
	if e.Kind == EventMatched || e.Kind == EventLate {
		return fmt.Sprintf("%-9s epoch=%d seq=%d latency=%s", e.Kind, e.Epoch, e.Seq, e.Latency)
	}
	return fmt.Sprintf("%-9s epoch=%d seq=%d", e.Kind, e.Epoch, e.Seq)
}

// Stats 是累计统计。
type Stats struct {
	Requests   int
	Duplicates int
	Matched    int
	Late       int
	Orphan     int
	Timeouts   int
	Evicted    int
}

type key struct{ epoch, seq uint64 }

type reqState struct {
	start    time.Time
	timedOut bool
	elem     *list.Element
}

// Tracker 按 (epoch, seq) 关联请求与响应。存储有界（maxPending），
// 超界时驱逐最旧挂起请求。非并发安全，由调用方串行化。
type Tracker struct {
	timeout    time.Duration
	maxPending int
	now        func() time.Time

	pending map[key]*reqState
	order   *list.List // front 最旧，用于驱逐与超时扫描
	events  []Event
	stats   Stats
}

// NewTracker 创建关联器。now 可注入便于测试；为 nil 时用 time.Now。
func NewTracker(timeout time.Duration, maxPending int, now func() time.Time) *Tracker {
	if now == nil {
		now = time.Now
	}
	return &Tracker{
		timeout:    timeout,
		maxPending: maxPending,
		now:        now,
		pending:    make(map[key]*reqState),
		order:      list.New(),
	}
}

// OnRequest 登记一个请求。同 (epoch, seq) 的重复请求视为重放：
// 保留原始开始时间，不产生新的挂起项。
func (t *Tracker) OnRequest(epoch, seq uint64) {
	t.sweep()
	k := key{epoch, seq}
	if _, ok := t.pending[k]; ok {
		t.stats.Duplicates++
		t.record(Event{Kind: EventDuplicate, Epoch: epoch, Seq: seq})
		return
	}
	for len(t.pending) >= t.maxPending {
		t.evictOldest()
	}
	st := &reqState{start: t.now()}
	st.elem = t.order.PushBack(k)
	t.pending[k] = st
	t.stats.Requests++
}

// OnResponse 关联一个响应。已完成的（epoch, seq）允许序号复用，
// 但旧 epoch 的迟到响应不会配到新请求上（键含 epoch）。
func (t *Tracker) OnResponse(epoch, seq uint64) {
	t.sweep()
	k := key{epoch, seq}
	st, ok := t.pending[k]
	if !ok {
		t.stats.Orphan++
		t.record(Event{Kind: EventOrphan, Epoch: epoch, Seq: seq})
		return
	}
	latency := t.now().Sub(st.start)
	t.remove(k, st)
	if st.timedOut || latency > t.timeout {
		t.stats.Late++
		t.record(Event{Kind: EventLate, Epoch: epoch, Seq: seq, Latency: latency})
		return
	}
	t.stats.Matched++
	t.record(Event{Kind: EventMatched, Epoch: epoch, Seq: seq, Latency: latency})
}

// OnFrame 按帧类型分发到 OnRequest / OnResponse。
func (t *Tracker) OnFrame(f Frame) {
	if f.Type == FrameRequest {
		t.OnRequest(f.Epoch, f.Seq)
	} else {
		t.OnResponse(f.Epoch, f.Seq)
	}
}

// Sweep 把超过超时阈值的挂起请求标记为超时（幂等，每项只报一次）。
// 超时项保留在有界存储中，使其迟到的响应被分类为 LATE 而非 ORPHAN。
func (t *Tracker) Sweep() { t.sweep() }

func (t *Tracker) sweep() {
	now := t.now()
	for e := t.order.Front(); e != nil; e = e.Next() {
		k := e.Value.(key)
		st := t.pending[k]
		if st.timedOut || now.Sub(st.start) <= t.timeout {
			continue
		}
		st.timedOut = true
		t.stats.Timeouts++
		t.record(Event{Kind: EventTimeout, Epoch: k.epoch, Seq: k.seq})
	}
}

func (t *Tracker) evictOldest() {
	front := t.order.Front()
	if front == nil {
		return
	}
	k := front.Value.(key)
	t.remove(k, t.pending[k])
	t.stats.Evicted++
	t.record(Event{Kind: EventEvicted, Epoch: k.epoch, Seq: k.seq})
}

func (t *Tracker) remove(k key, st *reqState) {
	t.order.Remove(st.elem)
	delete(t.pending, k)
}

func (t *Tracker) record(e Event) { t.events = append(t.events, e) }

// Events 返回累计事件的副本。
func (t *Tracker) Events() []Event {
	out := make([]Event, len(t.events))
	copy(out, t.events)
	return out
}

// Stats 返回累计统计。
func (t *Tracker) Stats() Stats { return t.stats }

// Pending 返回当前挂起请求数。
func (t *Tracker) Pending() int { return len(t.pending) }

type pendingEntry struct {
	Epoch         uint64
	Seq           uint64
	StartUnixNano int64
	TimedOut      bool
}

type checkpointData struct {
	TimeoutNano int64
	MaxPending  int
	Stats       Stats
	Pending     []pendingEntry // 按插入顺序，恢复后驱逐序不变
}

// Checkpoint 把挂起状态与统计序列化为字节，可持久化后由 Restore 恢复。
func (t *Tracker) Checkpoint() ([]byte, error) {
	cp := checkpointData{
		TimeoutNano: int64(t.timeout),
		MaxPending:  t.maxPending,
		Stats:       t.stats,
	}
	for e := t.order.Front(); e != nil; e = e.Next() {
		k := e.Value.(key)
		st := t.pending[k]
		cp.Pending = append(cp.Pending, pendingEntry{
			Epoch:         k.epoch,
			Seq:           k.seq,
			StartUnixNano: st.start.UnixNano(),
			TimedOut:      st.timedOut,
		})
	}
	var buf bytes.Buffer
	if err := gob.NewEncoder(&buf).Encode(cp); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// Restore 从 Checkpoint 字节恢复关联器。时钟起点随状态保留，
// 因此恢复后的延迟测量跨越 checkpoint 连续。
func Restore(data []byte, now func() time.Time) (*Tracker, error) {
	var cp checkpointData
	if err := gob.NewDecoder(bytes.NewReader(data)).Decode(&cp); err != nil {
		return nil, fmt.Errorf("restore: %w", err)
	}
	if cp.TimeoutNano <= 0 || cp.MaxPending <= 0 {
		return nil, fmt.Errorf("restore: invalid config timeout=%d maxPending=%d", cp.TimeoutNano, cp.MaxPending)
	}
	if len(cp.Pending) > cp.MaxPending {
		return nil, fmt.Errorf("restore: %d pending entries exceed maxPending %d", len(cp.Pending), cp.MaxPending)
	}
	tr := NewTracker(time.Duration(cp.TimeoutNano), cp.MaxPending, now)
	tr.stats = cp.Stats
	for _, pe := range cp.Pending {
		k := key{pe.Epoch, pe.Seq}
		if _, dup := tr.pending[k]; dup {
			return nil, fmt.Errorf("restore: duplicate pending key epoch=%d seq=%d", pe.Epoch, pe.Seq)
		}
		st := &reqState{start: time.Unix(0, pe.StartUnixNano), timedOut: pe.TimedOut}
		st.elem = tr.order.PushBack(k)
		tr.pending[k] = st
	}
	return tr, nil
}
