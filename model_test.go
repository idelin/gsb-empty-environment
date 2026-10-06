package dedup

import (
	"fmt"
	"math/rand"
	"testing"
)

// 全历史参考模型:永不回收,记录每个 ID 的首次有效载荷,
// 用与实现相同的显式来源进度判定过旧。随机操作序列下,
// 实现(带回收)的判定必须与模型(全历史)一致。
type model struct {
	progress map[string]int64
	history  map[string]entry // id -> 首次载荷与事件时间,永不删除
	wm       int64
	hasWM    bool
}

type modelOutcome int

const (
	mAccepted modelOutcome = iota
	mDuplicate
	mStale
)

type modelResult struct {
	outcome  modelOutcome
	payload  string
	conflict bool
}

func newModel() *model {
	return &model{
		progress: make(map[string]int64),
		history:  make(map[string]entry),
	}
}

func (m *model) reportProgress(source string, t int64) {
	if cur, ok := m.progress[source]; !ok || t > cur {
		m.progress[source] = t
	}
	// 水位 = 各来源进度最小值的单调推进,与实现保持一致。
	first := true
	var min int64
	for _, p := range m.progress {
		if first || p < min {
			min = p
			first = false
		}
	}
	if !first && (!m.hasWM || min > m.wm) {
		m.wm = min
		m.hasWM = true
	}
}

func (m *model) ingest(ev Event) modelResult {
	if e, ok := m.history[ev.ID]; ok {
		return modelResult{
			outcome:  mDuplicate,
			payload:  e.payload,
			conflict: e.payload != ev.Payload,
		}
	}
	if p, ok := m.progress[ev.Source]; ok && ev.EventTime <= p {
		return modelResult{outcome: mStale}
	}
	m.history[ev.ID] = entry{payload: ev.Payload, eventTime: ev.EventTime}
	return modelResult{outcome: mAccepted, payload: ev.Payload}
}

func (m *model) watermark() (int64, bool) {
	return m.wm, m.hasWM
}

// checkInvariants 在每一步操作后核对实现与模型的一致性。
func checkInvariants(t *testing.T, d *Deduper, m *model, step int) {
	t.Helper()

	wm, hasWM := d.Watermark()
	mwm, mHasWM := m.watermark()
	if wm != mwm || hasWM != mHasWM {
		t.Fatalf("step %d: watermark = (%d,%v), model = (%d,%v)", step, wm, hasWM, mwm, mHasWM)
	}

	live := 0
	for id, me := range m.history {
		payload, et, ok := d.Lookup(id)
		if ok {
			live++
			if payload != me.payload || et != me.eventTime {
				t.Fatalf("step %d: id %q stored (%q,%d), model has (%q,%d)",
					step, id, payload, et, me.payload, me.eventTime)
			}
			continue
		}
		// 已回收的条目必须满足:水位 > 事件时间+保留期。
		// 重放已回收 ID 时只能凭显式来源进度判过旧,不能当新事件,
		// 因此回收条件不成立却丢失条目即违反契约。
		if !hasWM || wm <= me.eventTime+d.retention {
			t.Fatalf("step %d: id %q missing but not reclaimable (wm=%d,%v et=%d)",
				step, id, wm, hasWM, me.eventTime)
		}
	}
	if d.Len() != live {
		t.Fatalf("step %d: len = %d, want %d", step, d.Len(), live)
	}
}

func checkResult(t *testing.T, step int, ev Event, got Result, want modelResult) {
	t.Helper()
	switch want.outcome {
	case mAccepted:
		if got.Outcome != Accepted || got.Payload != want.payload {
			t.Fatalf("step %d: %+v = %+v, model wants Accepted/%q", step, ev, got, want.payload)
		}
	case mDuplicate:
		// 实现可能已回收该 ID,此时凭来源进度判 Stale 也合法;
		// 但若判 Duplicate,首次载荷与冲突标记必须一致。绝不允许 Accepted。
		switch got.Outcome {
		case Duplicate:
			if got.Payload != want.payload || got.Conflict != want.conflict {
				t.Fatalf("step %d: %+v = %+v, model wants Duplicate/%q/conflict=%v",
					step, ev, got, want.payload, want.conflict)
			}
		case Stale:
		default:
			t.Fatalf("step %d: %+v = Accepted, model wants Duplicate (replayed id treated as new)", step, ev)
		}
	case mStale:
		if got.Outcome != Stale {
			t.Fatalf("step %d: %+v = %+v, model wants Stale", step, ev, got)
		}
	}
}

func TestAgainstFullHistoryModel(t *testing.T) {
	const (
		seeds     = 20
		steps     = 2000
		retention = 10
	)
	sources := []string{"s0", "s1", "s2"}

	for seed := int64(0); seed < seeds; seed++ {
		t.Run(fmt.Sprintf("seed=%d", seed), func(t *testing.T) {
			rng := rand.New(rand.NewSource(seed))
			d := New(retention)
			m := newModel()
			prog := make(map[string]int64)
			var sent []Event // 已发送过的事件,用于制造重放

			for step := 0; step < steps; step++ {
				op := rng.Intn(10)
				if op < 3 || len(sent) == 0 {
					// 上报进度:单源单调推进,水位随之前进并触发回收。
					s := sources[rng.Intn(len(sources))]
					prog[s] += int64(rng.Intn(8))
					d.ReportProgress(s, prog[s])
					m.reportProgress(s, prog[s])
				} else {
					var ev Event
					if op < 6 {
						// 重放一条历史事件(可能已被回收),半数概率改载荷制造冲突。
						ev = sent[rng.Intn(len(sent))]
						if rng.Intn(2) == 0 {
							ev.Payload = fmt.Sprintf("alt-%d", rng.Intn(1000))
						}
					} else {
						// 新事件:ID 取自小池子以制造同 ID 冲突,
						// 事件时间围绕来源进度抖动以制造迟到事件。
						s := sources[rng.Intn(len(sources))]
						ev = Event{
							Source:    s,
							ID:        fmt.Sprintf("id-%d", rng.Intn(12)),
							EventTime: prog[s] + int64(rng.Intn(25)) - 12,
							Payload:   fmt.Sprintf("p-%d", rng.Intn(1000)),
						}
						sent = append(sent, ev)
					}
					got := d.Ingest(ev)
					want := m.ingest(ev)
					checkResult(t, step, ev, got, want)
				}
				checkInvariants(t, d, m, step)
			}
		})
	}
}
