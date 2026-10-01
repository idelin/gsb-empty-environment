// Command demo reproduces the acceptance scenario: 1 writer thread
// densely transferring value between 1000 keys while 5 reader threads
// continuously run full-table checksum scans, verifying that every
// lock-free snapshot observes the conserved total (zero torn reads).
package main

import (
	"fmt"
	"math/rand"
	"sync"
	"sync/atomic"
	"time"

	"lockfreemvcc"
)

func main() {
	const (
		numKeys    = 1000
		perKey     = 1000
		numReaders = 5
		duration   = 3 * time.Second
	)
	const wantSum = int64(numKeys * perKey)

	store := lockfreemvcc.NewStore(numKeys)
	for i := 0; i < numKeys; i++ {
		store.Seed(i, perKey)
	}

	var commits, scans, mismatches atomic.Int64
	stop := make(chan struct{})
	var wg sync.WaitGroup

	// 1 writer thread: dense sum-preserving transfers, never waits on readers.
	wg.Add(1)
	go func() {
		defer wg.Done()
		rng := rand.New(rand.NewSource(time.Now().UnixNano()))
		for {
			select {
			case <-stop:
				return
			default:
			}
			from, to := rng.Intn(numKeys), rng.Intn(numKeys)
			if from == to {
				continue
			}
			store.Transfer(from, to, int64(rng.Intn(10)+1))
			commits.Add(1)
		}
	}()

	// 5 reader threads: continuous lock-free full-table checksum scans.
	for r := 0; r < numReaders; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				snap := store.Begin()
				var sum int64
				for k := 0; k < store.NumKeys(); k++ {
					sum += snap.Get(k)
				}
				snap.Close()
				scans.Add(1)
				if sum != wantSum {
					mismatches.Add(1)
				}
			}
		}()
	}

	time.Sleep(duration)
	close(stop)
	wg.Wait()

	c, sc, mm := commits.Load(), scans.Load(), mismatches.Load()
	fmt.Printf("duration        : %v\n", duration)
	fmt.Printf("keys            : %d (initial total %d)\n", numKeys, wantSum)
	fmt.Printf("writer commits  : %d (%.0f commits/s, never blocked by readers)\n", c, float64(c)/duration.Seconds())
	fmt.Printf("reader scans    : %d (%.0f scans/s across %d readers, lock-free)\n", sc, float64(sc)/duration.Seconds(), numReaders)
	fmt.Printf("torn snapshots  : %d\n", mm)
	if mm == 0 {
		fmt.Println("RESULT: PASS - every snapshot observed the conserved sum, zero torn reads")
	} else {
		fmt.Println("RESULT: FAIL - torn reads observed")
	}
}
