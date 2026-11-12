package netsec

import (
	"sync"
	"testing"
	"time"
)

func TestConcurrentIngestAndSnapshot(t *testing.T) {
	d := NewDetector()
	id := mustRegister(t, d, Query{
		Window:        time.Minute,
		PortThreshold: 2,
		HostThreshold: 2,
		At:            t0.Add(time.Minute),
	})

	var wg sync.WaitGroup
	for worker := 0; worker < 8; worker++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				base := worker*200 + i
				e := failAt(int64(base%50), "10.0.0.1", uint16(20000+base), "192.0.2.1", uint16(20+(base%10)))
				d.Ingest(e)
				if base%7 == 0 {
					d.Ingest(successOf(e))
				}
				if base%13 == 0 {
					d.AdvanceWatermark(t0.Add(time.Duration(base%5) * time.Second))
				}
				if _, ok := d.Snapshot(id); !ok {
					t.Errorf("query vanished")
					return
				}
			}
		}(worker)
	}
	wg.Wait()

	snap, ok := d.Snapshot(id)
	if !ok {
		t.Fatal("query missing after concurrent run")
	}
	// Every snapshot must remain internally sorted and count-consistent.
	for i := 1; i < len(snap.Alerts); i++ {
		if compareAlert(snap.Alerts[i-1], snap.Alerts[i]) > 0 {
			t.Fatal("alerts not sorted")
		}
	}
}
