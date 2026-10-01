package telemetry

import (
	"math/rand"
	"time"
)

// EventKind 区分可回放的事件类型。所有事件都在本地合成，
// 不依赖任何外部监控平台。
type EventKind int

const (
	EvSample  EventKind = iota // 接收一个样本
	EvCorrect                  // 标签更正：Labels -> NewLabels
	EvReclaim                  // 触发一次过期回收
)

// Event 是一条可回放的遥测事件。
type Event struct {
	Kind      EventKind
	At        time.Time
	Tenant    string
	Metric    string
	Labels    []Label // EvSample 的标签；EvCorrect 的旧标签
	Value     int64
	Bytes     int64
	NewLabels []Label // EvCorrect 的新标签
}

// ManualClock 是回放用的手动时钟。时间只前进不后退：
// 迟到样本按到达时刻入账，其旧代际已封存在过期代际中。
type ManualClock struct {
	t time.Time
}

// Now 返回当前时刻。
func (c *ManualClock) Now() time.Time { return c.t }

// Advance 把时钟推进到 t（若 t 更晚）。
func (c *ManualClock) Advance(t time.Time) {
	if t.After(c.t) {
		c.t = t
	}
}

// Replay 依序回放事件并返回最终一致性快照。
// 同一事件序列的回放结果完全确定。
func Replay(cfg Config, events []Event) Snapshot {
	clock := &ManualClock{}
	l := NewLimiter(cfg, clock.Now)
	for _, e := range events {
		clock.Advance(e.At)
		switch e.Kind {
		case EvSample:
			l.Ingest(Sample{
				Tenant: e.Tenant,
				Metric: e.Metric,
				Labels: e.Labels,
				Value:  e.Value,
				Bytes:  e.Bytes,
			})
		case EvCorrect:
			l.Correct(e.Tenant, e.Metric, e.Labels, e.NewLabels)
		case EvReclaim:
			l.Reclaim()
		}
	}
	return l.Snapshot()
}

// SynthEvents 用固定种子在本地确定性地合成事件流，可由夹具直接回放。
// 事件混合了正常样本、高基数突发、迟到样本（TTL 静默后再次出现）、
// 标签更正与显式回收，覆盖两层配额竞争。
func SynthEvents(seed int64, n int) []Event {
	rng := rand.New(rand.NewSource(seed))
	tenants := []string{"tenant-a", "tenant-b"}
	metrics := []string{"http_requests", "queue_depth", "cache_hits"}
	base := time.Unix(1700000000, 0)

	events := make([]Event, 0, n)
	for i := 0; i < n; i++ {
		at := base.Add(time.Duration(i) * 100 * time.Millisecond)
		tenant := tenants[rng.Intn(len(tenants))]
		metric := metrics[rng.Intn(len(metrics))]

		switch roll := rng.Intn(100); {
		case roll < 5:
			// 标签更正：把实例 inst-k 改写为 inst-(k+1)。
			k := rng.Intn(6)
			events = append(events, Event{
				Kind:   EvCorrect,
				At:     at,
				Tenant: tenant,
				Metric: metric,
				Labels: []Label{
					{Key: "instance", Value: itoa(k)},
					{Key: "job", Value: "ingest"},
				},
				NewLabels: []Label{
					{Key: "instance", Value: itoa(k + 1)},
					{Key: "job", Value: "ingest"},
				},
			})
		case roll < 8:
			events = append(events, Event{Kind: EvReclaim, At: at})
		default:
			// http_requests 基数高（0..11），其余指标基数低（0..3），
			// 制造租户级与指标级的额度竞争。
			card := 4
			if metric == "http_requests" {
				card = 12
			}
			inst := rng.Intn(card)
			labels := []Label{
				{Key: "job", Value: "ingest"},
				{Key: "instance", Value: itoa(inst)},
			}
			if rng.Intn(10) == 0 {
				labels = append(labels, Label{Key: "zone", Value: itoa(rng.Intn(2))})
			}
			events = append(events, Event{
				Kind:   EvSample,
				At:     at,
				Tenant: tenant,
				Metric: metric,
				Labels: labels,
				Value:  int64(rng.Intn(100) + 1),
				Bytes:  int64(rng.Intn(180) + 20),
			})
		}
	}
	return events
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var buf [8]byte
	pos := len(buf)
	for i > 0 {
		pos--
		buf[pos] = byte('0' + i%10)
		i /= 10
	}
	return string(buf[pos:])
}
