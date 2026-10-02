package wire

import (
	"runtime"
	"sync"
	"testing"
	"time"

	"pbwire/metrics"
)

type benchMsg struct {
	Seq  uint64 `pb:"1,varint"`
	Name string `pb:"2,string"`
}

func TestMeteredDecodeMetrics(t *testing.T) {
	reg := &metrics.Registry{}
	m := NewMetered(reg)
	good := NewEncoder().Varint(1, 1).String(2, "x").Bytes()
	bad := good[:len(good)-1] // length prefix intact, payload missing

	for i := 0; i < 3; i++ {
		var out benchMsg
		if err := m.Unmarshal(good, &out); err != nil {
			t.Fatalf("good decode %d: %v", i, err)
		}
	}
	var out benchMsg
	if err := m.Unmarshal(bad, &out); err == nil {
		t.Fatal("truncated input must fail")
	}

	snap := reg.Snapshot()
	if snap.Counters["decode_calls_total"] != 4 {
		t.Errorf("calls = %d, want 4", snap.Counters["decode_calls_total"])
	}
	if snap.Counters["decode_bytes_total"] != int64(4*len(good)-1) {
		t.Errorf("bytes = %d", snap.Counters["decode_bytes_total"])
	}
	if snap.Histograms["decode_latency_ms"].Count != 4 {
		t.Errorf("latency observations = %d", snap.Histograms["decode_latency_ms"].Count)
	}
	// First call misses the schema cache, the rest hit.
	if snap.HitRates["schema_cache"] != 0.75 {
		t.Errorf("hit rate = %v, want 0.75", snap.HitRates["schema_cache"])
	}
	if snap.Errors[ErrNegativeLength.String()] != 1 {
		t.Errorf("error tally = %v", snap.Errors)
	}
}

func TestMeteredConcurrent(t *testing.T) {
	m := NewMetered(nil)
	good := NewEncoder().Varint(1, 9).String(2, "concurrent").Bytes()
	bad := []byte{0x12, 0x05} // field 2 (string) declares 5 bytes, none follow

	const workers = 8
	const iters = 500
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for i := 0; i < iters; i++ {
				var out benchMsg
				if err := m.Unmarshal(good, &out); err != nil {
					t.Errorf("decode: %v", err)
					return
				}
				if out.Seq != 9 {
					t.Errorf("seq = %d", out.Seq)
					return
				}
				var sink benchMsg
				_ = m.Unmarshal(bad, &sink)
			}
		}(w)
	}
	wg.Wait()

	snap := m.Reg.Snapshot()
	total := int64(2 * workers * iters)
	if snap.Counters["decode_calls_total"] != total {
		t.Errorf("calls = %d, want %d", snap.Counters["decode_calls_total"], total)
	}
	if snap.Errors[ErrNegativeLength.String()] != int64(workers*iters) {
		t.Errorf("error tally = %v", snap.Errors)
	}
}

func TestMeteredNoLeak(t *testing.T) {
	before := runtime.NumGoroutine()
	m := NewMetered(nil)
	good := NewEncoder().Varint(1, 1).Bytes()
	var wg sync.WaitGroup
	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 300; i++ {
				var out benchMsg
				_ = m.Unmarshal(good, &out)
			}
		}()
	}
	wg.Wait()
	_ = m.Reg.Export()
	for i := 0; i < 50; i++ {
		if runtime.NumGoroutine() <= before {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("goroutine leak: before=%d after=%d", before, runtime.NumGoroutine())
}
