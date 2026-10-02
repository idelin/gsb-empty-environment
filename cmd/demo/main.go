// Command demo runs the OCC engine against the pessimistic lock store
// on a read-heavy hot-key workload and prints throughput + conflict
// statistics, then demonstrates a precise stale-ReadSet abort.
//
// To stay fair on noisy/shared machines the two engines are measured
// in alternating short rounds and the median round is reported.
package main

import (
	"fmt"
	"math/rand"
	"runtime"
	"sort"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"occ"
)

const (
	keys       = 64
	rounds     = 7
	roundTime  = 500 * time.Millisecond
	readsPerTx = 4
)

var keyNames [keys]string

func init() {
	for i := 0; i < keys; i++ {
		keyNames[i] = fmt.Sprintf("k%d", i)
	}
}

func occWorker(s *occ.Store, seed int64, stop <-chan struct{}, ops *uint64) {
	rng := rand.New(rand.NewSource(seed))
	var n uint64
	for {
		select {
		case <-stop:
			atomic.AddUint64(ops, n)
			return
		default:
		}
		if rng.Intn(100) < 5 { // 5% writes, 95% reads
			key := keyNames[rng.Intn(keys)]
			s.Transact(func(txn *occ.Txn) {
				v, _ := strconv.Atoi(txn.Read(key))
				txn.Write(key, strconv.Itoa(v+1))
			})
		} else {
			txn := s.Begin()
			for i := 0; i < readsPerTx; i++ {
				txn.Read(keyNames[rng.Intn(keys)])
			}
			txn.Commit()
		}
		n++
	}
}

func lockWorker(l *occ.LockStore, seed int64, stop <-chan struct{}, ops *uint64) {
	rng := rand.New(rand.NewSource(seed))
	var n uint64
	for {
		select {
		case <-stop:
			atomic.AddUint64(ops, n)
			return
		default:
		}
		if rng.Intn(100) < 5 {
			key := keyNames[rng.Intn(keys)]
			l.Transact(func(get func(string) string, set func(string, string)) {
				v, _ := strconv.Atoi(get(key))
				set(key, strconv.Itoa(v+1))
			})
		} else {
			for i := 0; i < readsPerTx; i++ {
				l.Get(keyNames[rng.Intn(keys)])
			}
		}
		n++
	}
}

func measure(workers int, occMode bool) (uint64, occ.Stats) {
	s := occ.NewStore()
	l := occ.NewLockStore()
	for i := 0; i < keys; i++ {
		key := keyNames[i]
		s.Put(key, "0")
		l.Put(key, "0")
	}
	stop := make(chan struct{})
	var ops uint64
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(seed int64) {
			defer wg.Done()
			if occMode {
				occWorker(s, seed, stop, &ops)
			} else {
				lockWorker(l, seed, stop, &ops)
			}
		}(int64(w))
	}
	time.Sleep(roundTime)
	close(stop)
	wg.Wait()
	return ops, s.Stats()
}

func median(xs []float64) float64 {
	sort.Float64s(xs)
	return xs[len(xs)/2]
}

func main() {
	workers := runtime.NumCPU()
	fmt.Printf("workload: read-heavy (95%% reads / 5%% writes), %d hot keys, %d reads/txn, %d workers\n",
		keys, readsPerTx, workers)
	fmt.Printf("measuring %d alternating rounds of %v each...\n\n", rounds, roundTime)

	var occTPS, lockTPS []float64
	var stats occ.Stats
	for r := 0; r < rounds; r++ {
		ops, st := measure(workers, true)
		stats = st
		occTPS = append(occTPS, float64(ops)/roundTime.Seconds())
		ops, _ = measure(workers, false)
		lockTPS = append(lockTPS, float64(ops)/roundTime.Seconds())
	}

	occMed, lockMed := median(occTPS), median(lockTPS)
	fmt.Printf("OCC : %10.0f txn/s (median)   commits=%d aborts=%d conflictRate=%.2f%%\n",
		occMed, stats.Commits, stats.Aborts, 100*stats.ConflictRate())
	fmt.Printf("LOCK: %10.0f txn/s (median)\n", lockMed)
	fmt.Printf("OCC speedup: %.2fx\n\n", occMed/lockMed)

	// Deterministic stale-ReadSet abort demo.
	s := occ.NewStore()
	s.Put("balance", "100")
	t1 := s.Begin()
	t2 := s.Begin()
	t1.Read("balance")
	t2.Read("balance")
	t1.Write("balance", "80")
	t2.Write("balance", "60")
	c1, c2 := t1.Commit(), t2.Commit()
	fmt.Printf("overlapping writes: t1.Commit()=%v, t2.Commit()=%v (t2's ReadSet version went stale)\n", c1, c2)
	fmt.Printf("final balance=%q (t2 rolled back cleanly, no lost update, no partial write)\n",
		s.Get("balance"))
}
