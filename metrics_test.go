package cachebench

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestHistogramPercentiles(t *testing.T) {
	h := NewHistogram()
	for i := 0; i < 100; i++ {
		h.Record(50 * time.Microsecond)
	}
	for i := 0; i < 100; i++ {
		h.Record(500 * time.Microsecond)
	}
	h.Record(5 * time.Millisecond)

	if got := h.Count(); got != 201 {
		t.Fatalf("count = %d, want 201", got)
	}
	if got := h.MinUs(); got != 50 {
		t.Fatalf("min = %dus, want 50us", got)
	}
	if got := h.MaxUs(); got != 5000 {
		t.Fatalf("max = %dus, want 5000us", got)
	}
	// P50 落在第 101 个样本（500us 桶）。
	if got := h.Percentile(50); got != 500 {
		t.Fatalf("p50 = %dus, want 500us", got)
	}
	if got := h.Percentile(99); got != 500 {
		t.Fatalf("p99 = %dus, want 500us", got)
	}
	if got := h.Percentile(99.9); got != 5000 {
		t.Fatalf("p99.9 = %dus, want 5000us", got)
	}
	mean := h.MeanUs()
	want := float64(100*50+100*500+5000) / 201
	if mean < want-1 || mean > want+1 {
		t.Fatalf("mean = %v, want ~%v", mean, want)
	}
}

func TestHistogramMicrosecondResolution(t *testing.T) {
	h := NewHistogram()
	// 每个微秒桶放一个样本：0us .. 999us。
	for us := 0; us < 1000; us++ {
		h.Record(time.Duration(us) * time.Microsecond)
	}
	// 第 500 个样本落在 499us 桶（1us 分辨率）。
	if got := h.Percentile(50); got != 499 {
		t.Fatalf("p50 = %dus, want 499us (microsecond resolution)", got)
	}
	if got := h.Percentile(99); got != 989 {
		t.Fatalf("p99 = %dus, want 989us", got)
	}
}

func TestWindowSeries(t *testing.T) {
	ws := NewWindowSeries(time.Hour, 4)
	ws.RecordGet(true)
	ws.RecordGet(true)
	ws.RecordGet(false)
	ws.RecordSet(3)
	snap := ws.Snapshot()
	w := snap[0]
	if w.Hits != 2 || w.Misses != 1 || w.Sets != 1 || w.Evictions != 3 {
		t.Fatalf("window = %+v", w)
	}
	for i := 1; i < 4; i++ {
		if snap[i] != (Window{}) {
			t.Fatalf("window %d not empty: %+v", i, snap[i])
		}
	}
}

func TestMetricsCounters(t *testing.T) {
	m := NewMetrics(time.Minute, 4)
	m.RecordGet(true, 10*time.Microsecond)
	m.RecordGet(false, 20*time.Microsecond)
	m.RecordSet(30*time.Microsecond, 2)
	if m.Hits() != 1 || m.Misses() != 1 || m.Sets() != 1 || m.Evictions() != 2 {
		t.Fatalf("counters: hits=%d misses=%d sets=%d evictions=%d",
			m.Hits(), m.Misses(), m.Sets(), m.Evictions())
	}
	if got := m.HitRate(); got != 0.5 {
		t.Fatalf("hit rate = %v, want 0.5", got)
	}
}

func TestReportJSON(t *testing.T) {
	m := NewMetrics(time.Minute, 4)
	m.RecordGet(true, 10*time.Microsecond)
	m.RecordGet(false, 20*time.Microsecond)
	m.RecordSet(30*time.Microsecond, 1)
	c := NewCache(1024, m)
	c.Set("a", []byte("b"))

	report := BuildReport(m, c.MemoryStats())
	data, err := report.JSON()
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var decoded map[string]interface{}
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	for _, key := range []string{"totals", "get_latency", "set_latency", "memory", "health", "analysis", "windows"} {
		if _, ok := decoded[key]; !ok {
			t.Fatalf("report missing key %q", key)
		}
	}
	if !strings.Contains(string(data), "p99_us") {
		t.Fatal("report missing p99_us field")
	}
	if !strings.Contains(string(data), "cache_fragmentation_rate") {
		t.Fatal("report missing cache_fragmentation_rate field")
	}
}
