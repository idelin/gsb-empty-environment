// Demo: continuous writes with concurrent incremental checkpoints,
// verified recovery, and precise localization of a corrupted chunk.
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"lsmcheckpoint/checkpoint"
)

func main() {
	dir, err := os.MkdirTemp("", "ckpt-demo")
	if err != nil {
		panic(err)
	}
	defer os.RemoveAll(dir)

	store := checkpoint.NewStore()
	cp, err := checkpoint.NewCheckpointer(store, dir)
	if err != nil {
		panic(err)
	}

	const total = 100_000
	latencies := make([]time.Duration, 0, total)
	var mu sync.Mutex
	var metas []*checkpoint.Meta

	// Checkpoint trigger: one incremental checkpoint every 20k writes.
	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		next := uint64(20_000)
		for {
			select {
			case <-stop:
				return
			default:
			}
			if store.Version() >= next {
				m, err := cp.Checkpoint()
				if err != nil {
					panic(err)
				}
				mu.Lock()
				metas = append(metas, m)
				mu.Unlock()
				next += 20_000
			} else {
				time.Sleep(time.Millisecond)
			}
		}
	}()

	// Writer: 100k KV pairs, recording per-write latency.
	for i := 0; i < total; i++ {
		start := time.Now()
		store.Put(fmt.Sprintf("key-%d", i), fmt.Sprintf("value-%d", i))
		mu.Lock()
		latencies = append(latencies, time.Since(start))
		mu.Unlock()
	}
	close(stop)
	wg.Wait()
	final, err := cp.Checkpoint()
	if err != nil {
		panic(err)
	}
	metas = append(metas, final)

	sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })
	fmt.Printf("== writes ==\n")
	fmt.Printf("wrote %d KV pairs, p50=%v p99=%v max=%v\n",
		total, latencies[len(latencies)/2], latencies[len(latencies)*99/100], latencies[len(latencies)-1])

	fmt.Printf("\n== checkpoints ==\n")
	for _, m := range metas {
		fmt.Printf("ckpt-%06d: version=%-6d delta-pages=%-3d chunks=%-2d chain=%x...\n",
			m.ID, m.Version, m.NumPages, m.NumChunks, m.ChainHash[:8])
	}

	// Verified recovery from the checkpoint chain.
	var paths []string
	for _, m := range metas {
		paths = append(paths, m.Path)
	}
	state, err := checkpoint.Recover(paths)
	if err != nil {
		panic(err)
	}
	fmt.Printf("\n== recovery ==\n")
	fmt.Printf("recovered %d keys at version %d, integrity verified against hash chain\n",
		len(state.KV), state.Version)

	// Corrupt the 3rd chunk of the first checkpoint and try again.
	raw, err := os.ReadFile(metas[0].Path)
	if err != nil {
		panic(err)
	}
	off, length, err := checkpoint.LocateChunk(raw, 2)
	if err != nil {
		panic(err)
	}
	raw[off+length/2] ^= 0xFF
	badDir, err := os.MkdirTemp("", "ckpt-bad")
	if err != nil {
		panic(err)
	}
	defer os.RemoveAll(badDir)
	badPath := filepath.Join(badDir, filepath.Base(metas[0].Path))
	if err := os.WriteFile(badPath, raw, 0o644); err != nil {
		panic(err)
	}
	badPaths := append([]string{badPath}, paths[1:]...)

	fmt.Printf("\n== corrupted chunk #3 ==\n")
	if _, err := checkpoint.Recover(badPaths); err != nil {
		fmt.Printf("recovery rejected: %v\n", err)
	} else {
		fmt.Println("ERROR: corruption was not detected!")
		os.Exit(1)
	}
}
