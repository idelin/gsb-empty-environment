package wire

import (
	"reflect"
	"sync"
	"time"

	"pbwire/metrics"
)

// LatencyBuckets are default histogram bounds, in milliseconds, tuned
// for message-decode latencies from microseconds to seconds.
var LatencyBuckets = []float64{0.01, 0.05, 0.1, 0.5, 1, 5, 10, 50, 100, 500, 1000}

// Metered wraps the reflection decoder with fine-grained instrumentation:
// per-call latency (histogram + sliding-window percentiles), byte
// throughput, schema cache hit rate and per-code error tallies.
type Metered struct {
	Reg      *metrics.Registry
	MaxDepth int

	mu   sync.Mutex
	seen map[reflect.Type]bool
}

// NewMetered creates a Metered writing into reg (a fresh registry when
// nil) with DefaultMaxDepth nesting.
func NewMetered(reg *metrics.Registry) *Metered {
	if reg == nil {
		reg = &metrics.Registry{}
	}
	return &Metered{Reg: reg, MaxDepth: DefaultMaxDepth, seen: make(map[reflect.Type]bool)}
}

// Unmarshal decodes buf into out exactly like package-level Unmarshal,
// recording metrics around the call.
func (m *Metered) Unmarshal(buf []byte, out interface{}) error {
	start := time.Now()
	err := UnmarshalDepth(buf, out, m.MaxDepth)
	elapsedMs := float64(time.Since(start).Nanoseconds()) / 1e6

	m.Reg.Histogram("decode_latency_ms", LatencyBuckets...).Observe(elapsedMs)
	m.Reg.Window("decode_latency_window", time.Minute).Observe(elapsedMs)
	m.Reg.Counter("decode_calls_total").Inc()
	m.Reg.Counter("decode_bytes_total").Add(int64(len(buf)))
	m.Reg.Gauge("decode_last_latency_us").Set(int64(elapsedMs * 1000))

	m.mu.Lock()
	if m.seen == nil {
		m.seen = make(map[reflect.Type]bool)
	}
	t := reflect.TypeOf(out)
	_, known := m.seen[t]
	m.seen[t] = true
	m.mu.Unlock()
	if known {
		m.Reg.HitRate("schema_cache").Hit()
	} else {
		m.Reg.HitRate("schema_cache").Miss()
	}

	if err != nil {
		m.Reg.Errors().Inc(CodeOf(err).String())
	}
	return err
}
