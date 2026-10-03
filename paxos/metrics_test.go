package paxos

import (
	"bytes"
	"encoding/json"
	"math"
	"sync"
	"testing"
	"time"
)

// TestHistogramExactPercentiles verifies min/max/mean and nearest-rank
// percentiles over a known distribution.
func TestHistogramExactPercentiles(t *testing.T) {
	h := NewHistogram(0)
	for i := 1; i <= 100; i++ {
		h.Observe(float64(i))
	}
	s := h.Summary()
	if s.Count != 100 || s.Min != 1 || s.Max != 100 {
		t.Fatalf("summary=%+v", s)
	}
	if s.Mean != 50.5 {
		t.Fatalf("mean=%v, want 50.5", s.Mean)
	}
	if s.P50 != 50 || s.P90 != 90 || s.P99 != 99 {
		t.Fatalf("p50=%v p90=%v p99=%v", s.P50, s.P90, s.P99)
	}
}

// TestHistogramEmpty covers the zero-observation boundary.
func TestHistogramEmpty(t *testing.T) {
	s := NewHistogram(0).Summary()
	if s != (HistSummary{}) {
		t.Fatalf("empty summary=%+v, want zero value", s)
	}
}

// TestHistogramSingle covers the single-sample boundary.
func TestHistogramSingle(t *testing.T) {
	h := NewHistogram(0)
	h.Observe(4.2)
	s := h.Summary()
	if s.Count != 1 || s.Min != 4.2 || s.Max != 4.2 || s.Mean != 4.2 ||
		s.P50 != 4.2 || s.P90 != 4.2 || s.P99 != 4.2 {
		t.Fatalf("summary=%+v", s)
	}
}

// TestHistogramCap: eviction bounds retained samples but keeps exact
// count/sum/min/max over all observations.
func TestHistogramCap(t *testing.T) {
	h := NewHistogram(10)
	for i := 1; i <= 25; i++ {
		h.Observe(float64(i))
	}
	s := h.Summary()
	if s.Count != 25 {
		t.Fatalf("count=%d, want 25", s.Count)
	}
	if s.Mean != 13 || s.Min != 1 || s.Max != 25 {
		t.Fatalf("summary=%+v", s)
	}
	if got := len(h.rawValues()); got != 10 {
		t.Fatalf("retained=%d, want 10", got)
	}
	// Percentiles are computed over the retained window (16..25).
	if s.P50 != 20 {
		t.Fatalf("windowed p50=%v, want 20", s.P50)
	}
}

// TestHitRateBoundaries: no samples -> 0; only misses -> 0; only hits -> 1.
func TestHitRateBoundaries(t *testing.T) {
	m := NewMetrics()
	if r := m.HitRate("none"); r != 0 {
		t.Fatalf("empty hit rate=%v, want 0", r)
	}
	m.Miss("x")
	m.Miss("x")
	if r := m.HitRate("x"); r != 0 {
		t.Fatalf("all-miss hit rate=%v, want 0", r)
	}
	m.Hit("y")
	if r := m.HitRate("y"); r != 1 {
		t.Fatalf("all-hit hit rate=%v, want 1", r)
	}
	m.Hit("z")
	m.Miss("z")
	m.Miss("z")
	if r := m.HitRate("z"); r != 1.0/3.0 {
		t.Fatalf("hit rate=%v, want 1/3", r)
	}
}

// TestErrorCodeTally verifies error-code accounting and naming.
func TestErrorCodeTally(t *testing.T) {
	m := NewMetrics()
	if got := len(m.Errors()); got != 0 {
		t.Fatalf("empty errors=%d, want 0", got)
	}
	m.RecordError(ErrNoQuorum)
	m.RecordError(ErrNoQuorum)
	m.RecordError(ErrBallotTooLow)
	errs := m.Errors()
	if errs[ErrNoQuorum] != 2 || errs[ErrBallotTooLow] != 1 {
		t.Fatalf("errors=%v", errs)
	}
	if ErrNoQuorum.String() != "NO_QUORUM" || ErrNone.String() != "NONE" {
		t.Fatalf("bad code names: %s %s", ErrNoQuorum, ErrNone)
	}
	if ErrCode(999).String() == "" {
		t.Fatal("unknown code must still stringify")
	}
}

// TestSeriesRingBuffer: the time series keeps the newest cap samples in
// chronological order, with nanosecond-precision timestamps.
func TestSeriesRingBuffer(t *testing.T) {
	m := NewMetricsWithCap(3)
	base := time.Unix(1700000000, 0)
	var tick int64
	m.now = func() time.Time {
		tick++
		return base.Add(time.Duration(tick) * time.Nanosecond)
	}
	for i := 0; i < 5; i++ {
		m.Observe("lat", time.Duration(i+1)*time.Microsecond)
	}
	all := m.SeriesAll()
	if len(all) != 3 {
		t.Fatalf("series len=%d, want 3", len(all))
	}
	// Oldest first: retained observations are i=2,3,4 -> ticks 3,4,5.
	for i, s := range all {
		wantT := base.Add(time.Duration(i+3) * time.Nanosecond)
		if !s.T.Equal(wantT) {
			t.Fatalf("sample %d t=%v, want %v", i, s.T, wantT)
		}
		if s.Name != "lat" {
			t.Fatalf("sample %d name=%s", i, s.Name)
		}
	}
	if all[0].Value != 3e-6 {
		t.Fatalf("oldest retained value=%v, want 3e-6", all[0].Value)
	}
	// Filtered series view.
	if got := len(m.Series("lat")); got != 3 {
		t.Fatalf("filtered len=%d, want 3", got)
	}
	if got := len(m.Series("absent")); got != 0 {
		t.Fatalf("absent series len=%d, want 0", got)
	}
}

// TestSeriesTimestampsMonotonic: real-clock samples are non-decreasing.
func TestSeriesTimestampsMonotonic(t *testing.T) {
	m := NewMetrics()
	for i := 0; i < 50; i++ {
		m.Observe("l", time.Microsecond)
	}
	all := m.SeriesAll()
	for i := 1; i < len(all); i++ {
		if all[i].T.Before(all[i-1].T) {
			t.Fatalf("timestamps not monotonic at %d", i)
		}
	}
}

// TestObserveValueRecordsScalars: non-latency points enter the series but
// not any histogram.
func TestObserveValueRecordsScalars(t *testing.T) {
	m := NewMetrics()
	m.ObserveValue("queue.depth", 7)
	if got := len(m.Series("queue.depth")); got != 1 {
		t.Fatalf("series len=%d, want 1", got)
	}
	if _, ok := m.Histogram("queue.depth"); ok {
		t.Fatal("scalar observation must not create a histogram")
	}
	if _, ok := m.Histogram("absent"); ok {
		t.Fatal("absent histogram must report ok=false")
	}
}

// TestSnapshotIsolation: mutating a snapshot must not affect live metrics.
func TestSnapshotIsolation(t *testing.T) {
	m := NewMetrics()
	m.Inc("c", 5)
	m.RecordError(ErrTimeout)
	m.Hit("lease")
	m.Observe("lat", time.Millisecond)
	snap := m.Snapshot(1)
	snap.Counters["c"] = -1
	snap.Errors["X"] = -1
	snap.HitRates["lease"] = -1
	if m.Counter("c") != 5 {
		t.Fatal("snapshot shares counter state with live metrics")
	}
	if m.Errors()[ErrTimeout] != 1 || m.HitRate("lease") != 1 {
		t.Fatal("snapshot shares error/hit state with live metrics")
	}
	if snap.NodeID != 1 || snap.SeriesLen != 1 {
		t.Fatalf("snapshot=%+v", snap)
	}
	if snap.Uptime < 0 {
		t.Fatalf("uptime=%v", snap.Uptime)
	}
}

// TestAggregateSnapshots verifies cross-node statistical aggregation.
func TestAggregateSnapshots(t *testing.T) {
	m1, m2 := NewMetrics(), NewMetrics()
	m1.Inc("a", 1)
	m2.Inc("a", 2)
	m2.Inc("b", 3)
	m1.RecordError(ErrNoQuorum)
	m2.RecordError(ErrNoQuorum)
	m2.RecordError(ErrPartition)
	m1.Hit("lease")
	m1.Hit("lease")
	m1.Miss("lease") // 2/3
	m2.Hit("lease")  // 1/1
	for i := 1; i <= 3; i++ {
		m1.Observe("lat", time.Duration(i)*time.Second)
	}
	m2.Observe("lat", 10*time.Second)

	agg := AggregateSnapshots(m1.Snapshot(0), m2.Snapshot(1))
	if agg.Nodes != 2 {
		t.Fatalf("nodes=%d", agg.Nodes)
	}
	if agg.Counters["a"] != 3 || agg.Counters["b"] != 3 {
		t.Fatalf("counters=%v", agg.Counters)
	}
	if agg.Errors["NO_QUORUM"] != 2 || agg.Errors["PARTITION"] != 1 {
		t.Fatalf("errors=%v", agg.Errors)
	}
	want := (2.0/3.0 + 1.0) / 2.0
	if math.Abs(agg.HitRates["lease"]-want) > 1e-9 {
		t.Fatalf("lease rate=%v, want %v", agg.HitRates["lease"], want)
	}
	h := agg.Histograms["lat"]
	if h.Count != 4 || h.Min != 1 || h.Max != 10 {
		t.Fatalf("pooled histogram=%+v", h)
	}
	if h.Mean != 4 { // (1+2+3+10)/4
		t.Fatalf("pooled mean=%v, want 4", h.Mean)
	}
}

// TestAggregateEmpty covers the zero-snapshot boundary.
func TestAggregateEmpty(t *testing.T) {
	agg := AggregateSnapshots()
	if agg.Nodes != 0 || len(agg.Counters) != 0 || len(agg.Histograms) != 0 {
		t.Fatalf("empty aggregate=%+v", agg)
	}
}

// TestExportJSON verifies the export format is valid, complete JSON.
func TestExportJSON(t *testing.T) {
	m := NewMetrics()
	m.Inc("propose.ok", 7)
	m.RecordError(ErrNoQuorum)
	m.Hit("lease")
	m.Miss("lease")
	m.Observe("paxos.propose.e2e", 2*time.Millisecond)
	snap := m.Snapshot(3)

	var buf bytes.Buffer
	if err := ExportJSON(&buf, snap); err != nil {
		t.Fatalf("export: %v", err)
	}
	var decoded map[string]interface{}
	if err := json.Unmarshal(buf.Bytes(), &decoded); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	for _, key := range []string{"nodeID", "takenAt", "uptimeSeconds", "counters", "errors", "hitRates", "histograms", "seriesLen"} {
		if _, ok := decoded[key]; !ok {
			t.Fatalf("exported JSON missing key %q", key)
		}
	}
	counters := decoded["counters"].(map[string]interface{})
	if counters["propose.ok"] != 7.0 {
		t.Fatalf("counters=%v", counters)
	}
	errs := decoded["errors"].(map[string]interface{})
	if errs["NO_QUORUM"] != 1.0 {
		t.Fatalf("errors=%v", errs)
	}
	hists := decoded["histograms"].(map[string]interface{})
	e2e := hists["paxos.propose.e2e"].(map[string]interface{})
	if e2e["count"] != 1.0 || e2e["p99"] != 0.002 {
		t.Fatalf("histogram=%v", e2e)
	}

	var abuf bytes.Buffer
	if err := ExportAggregateJSON(&abuf, AggregateSnapshots(snap)); err != nil {
		t.Fatalf("aggregate export: %v", err)
	}
	var agg map[string]interface{}
	if err := json.Unmarshal(abuf.Bytes(), &agg); err != nil {
		t.Fatalf("invalid aggregate JSON: %v", err)
	}
	if agg["nodes"] != 1.0 {
		t.Fatalf("aggregate=%v", agg)
	}
}

// TestMetricsConcurrency exercises every Metrics API from many goroutines;
// run with -race. Final tallies must be exact.
func TestMetricsConcurrency(t *testing.T) {
	m := NewMetrics()
	const workers = 16
	const iters = 200
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < iters; i++ {
				m.Inc("ops", 1)
				m.Hit("lease")
				m.Miss("lease")
				m.Observe("lat", time.Duration(i)*time.Microsecond)
				m.RecordError(ErrNoQuorum)
				m.ObserveValue("gauge", float64(w))
				_ = m.Snapshot(w)
				_ = m.SeriesAll()
				_ = m.HitRate("lease")
				_, _ = m.Histogram("lat")
			}
		}(w)
	}
	wg.Wait()
	if got := m.Counter("ops"); got != workers*iters {
		t.Fatalf("ops=%d, want %d", got, workers*iters)
	}
	if got := m.Errors()[ErrNoQuorum]; got != workers*iters {
		t.Fatalf("errors=%d, want %d", got, workers*iters)
	}
	if r := m.HitRate("lease"); r != 0.5 {
		t.Fatalf("hit rate=%v, want 0.5", r)
	}
	if s, _ := m.Histogram("lat"); s.Count != workers*iters {
		t.Fatalf("histogram count=%d, want %d", s.Count, workers*iters)
	}
	if got := len(m.SeriesAll()); got != DefaultSeriesCap {
		t.Fatalf("series len=%d, want cap %d", got, DefaultSeriesCap)
	}
}

// TestMetricsNoLeak: Metrics spawns no background goroutines.
func TestMetricsNoLeak(t *testing.T) {
	before := countGoroutines()
	m := NewMetrics()
	for i := 0; i < 100; i++ {
		m.Observe("x", time.Millisecond)
	}
	_ = m.Snapshot(0)
	if after := countGoroutines(); after != before {
		t.Fatalf("goroutines before=%d after=%d", before, after)
	}
}
