package cachemodel

import (
	"math/rand"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestConcurrentModelCrossCheck hammers one page with concurrent writers and
// a writeback worker that performs random short writes, then evicts. Every
// cache event is logged at its linearization point and replayed through the
// per-byte version Model, which must agree on the final dirty set, page
// contents, and disk image.
func TestConcurrentModelCrossCheck(t *testing.T) {
	const (
		pageSize    = 256
		numWriters  = 4
		writesEach  = 200
		maxWriteLen = 64
	)
	seed := time.Now().UnixNano()
	t.Logf("seed=%d", seed)

	page := NewPage(pageSize)
	disk := NewDisk(pageSize)

	var logMu sync.Mutex
	var events []Event
	record := func(e Event) {
		logMu.Lock()
		events = append(events, e)
		logMu.Unlock()
	}
	page.observer = record

	var stop int32
	var writers sync.WaitGroup

	// Concurrent writers.
	for w := 0; w < numWriters; w++ {
		writers.Add(1)
		go func(id int) {
			defer writers.Done()
			rng := rand.New(rand.NewSource(seed + int64(id)))
			for i := 0; i < writesEach; i++ {
				off := rng.Intn(pageSize)
				n := 1 + rng.Intn(maxWriteLen)
				if off+n > pageSize {
					n = pageSize - off
				}
				buf := make([]byte, n)
				rng.Read(buf)
				page.Write(off, buf)
			}
		}(w)
	}

	// Writeback worker: captures version + dirty set, then persists random
	// sub-ranges (short writes) before completing.
	workerDone := make(chan struct{})
	go func() {
		defer close(workerDone)
		rng := rand.New(rand.NewSource(seed + 999))
		for {
			wb, ok := page.BeginWriteback()
			if !ok {
				if atomic.LoadInt32(&stop) == 1 && page.IsClean() {
					return
				}
				runtime.Gosched()
				continue
			}
			var written []Interval
			for _, iv := range wb.Dirty {
				// Short write: persist only a random sub-range.
				span := iv.End - iv.Start
				s := iv.Start + rng.Intn(span)
				e := s + 1 + rng.Intn(iv.End-s)
				if rng.Intn(4) == 0 {
					s, e = iv.Start, iv.End // sometimes the full range
				}
				disk.Write(s, wb.Data[s:e])
				record(Event{Op: OpDiskWrite, Off: s, Data: append([]byte(nil), wb.Data[s:e]...)})
				written = append(written, Interval{s, e})
			}
			page.CompleteWriteback(wb, written)
		}
	}()

	// Evictor: once writers finish, eviction must wait for the worker to
	// drain the page; the model asserts cleanliness at that exact point.
	writers.Wait()
	evictDone := make(chan struct{})
	go func() {
		page.Evict()
		close(evictDone)
	}()
	<-evictDone
	atomic.StoreInt32(&stop, 1)
	<-workerDone

	// Replay the exact event sequence against the per-byte version model.
	logMu.Lock()
	evs := append([]Event(nil), events...)
	logMu.Unlock()
	model := NewModel(pageSize)
	for _, e := range evs {
		model.Apply(e)
	}
	if err := model.CheckPage(page); err != nil {
		t.Fatalf("seed=%d: %v", seed, err)
	}
	if err := model.CheckDisk(disk); err != nil {
		t.Fatalf("seed=%d: %v", seed, err)
	}
	if !page.IsClean() {
		t.Fatalf("seed=%d: page not clean at end", seed)
	}
}
