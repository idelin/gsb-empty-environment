// Demo: continuous writes + live checkpoint + incremental diff +
// corruption injection and precise rejection on restore.
package main

import (
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"lscheckpoint"
)

func main() {
	dir, err := os.MkdirTemp("", "lscheckpoint-demo")
	if err != nil {
		log.Fatal(err)
	}
	defer os.RemoveAll(dir)

	const total = 100000
	sm := lscheckpoint.NewStateMachine()
	cp := lscheckpoint.NewCheckpointer(dir)

	// 1. Continuous writer: 100k KV pairs, recording per-write latency.
	lat := make([]int64, total)
	var written atomic.Int64
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < total; i++ {
			start := time.Now()
			sm.Put(fmt.Sprintf("key-%08d", i), []byte(fmt.Sprintf("value-%08d", i)))
			lat[i] = time.Since(start).Nanoseconds()
			written.Store(int64(i + 1))
		}
	}()

	// 2. Trigger a full checkpoint mid-stream.
	for written.Load() < total/2 {
		time.Sleep(time.Millisecond)
	}
	snap := sm.Snapshot()
	m, err := cp.Checkpoint(snap, false)
	if err != nil {
		log.Fatal(err)
	}
	ckptEnd := written.Load()
	wg.Wait()
	fmt.Printf("[checkpoint] full checkpoint at seq=%d, chunks=%d, root=%s...\n",
		m.Seq, len(m.Chunks), m.RootHash[:16])

	stats := func(lo, hi int64) (avg, max time.Duration) {
		var sum, mx int64
		for i := lo; i < hi; i++ {
			sum += lat[i]
			if lat[i] > mx {
				mx = lat[i]
			}
		}
		return time.Duration(sum / (hi - lo)), time.Duration(mx)
	}
	baseAvg, _ := stats(1000, 40000)
	durAvg, durMax := stats(int64(m.Seq), ckptEnd)
	fmt.Printf("[latency] baseline avg=%v | during-checkpoint avg=%v max=%v\n",
		baseAvg, durAvg, durMax)

	// 3. Incremental diff package after more writes.
	for i := 0; i < 100; i++ {
		sm.Put(fmt.Sprintf("key-%08d", i*7), []byte("updated"))
	}
	mi, err := cp.Checkpoint(sm.Snapshot(), true)
	if err != nil {
		log.Fatal(err)
	}
	changed := 0
	for _, c := range mi.Chunks {
		changed += len(c.Pages)
	}
	fmt.Printf("[incremental] diff package at seq=%d: %d changed pages in %d chunks (base=%s)\n",
		mi.Seq, changed, len(mi.Chunks), mi.Base)

	// 4. Restore from the clean incremental chain first.
	restored, err := lscheckpoint.Restore(filepath.Join(dir, fmt.Sprintf("ckpt-%06d", mi.Seq)))
	if err != nil {
		log.Fatal(err)
	}
	v, _ := restored.Get("key-00000000")
	fmt.Printf("[restore] incremental chain OK: %d keys, key-00000000=%q\n", restored.Len(), v)

	// 5. Corrupt the 3rd chunk of the full checkpoint, then restore.
	ckptDir := filepath.Join(dir, fmt.Sprintf("ckpt-%06d", m.Seq))
	victim := filepath.Join(ckptDir, "chunk-000002.bin")
	body, _ := os.ReadFile(victim)
	body[len(body)/2] ^= 0xFF
	os.WriteFile(victim, body, 0o644)
	fmt.Printf("[corrupt] flipped bits in 3rd chunk: %s\n", filepath.Base(victim))

	_, err = lscheckpoint.Restore(ckptDir)
	var cerr *lscheckpoint.CorruptionError
	switch {
	case errors.As(err, &cerr):
		fmt.Printf("[restore] REJECTED bad data, located chunk %d\n  -> %v\n", cerr.ChunkIndex, err)
	default:
		log.Fatalf("expected corruption to be detected, got %v", err)
	}
}
