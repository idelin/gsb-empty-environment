// demo 运行一个读多写少的高并发多键事务负载，对比 OCC 引擎与全局锁方案的吞吐，
// 并打印 OCC 的冲突率统计。
package main

import (
	"fmt"
	"math/rand"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"occexample/lockkv"
	"occexample/occ"
)

const (
	keys       = 1024
	txnKeys    = 4 // 每个事务读取的键数
	workers    = 16
	duration   = 2 * time.Second
	writeRatio = 5 // 5% 的事务为写事务
)

func runOCC(e *occ.Engine) uint64 {
	var ops uint64
	var stop int32
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(seed int64) {
			defer wg.Done()
			r := rand.New(rand.NewSource(seed))
			for atomic.LoadInt32(&stop) == 0 {
				write := r.Intn(100) < writeRatio
				tx := e.Begin()
				for i := 0; i < txnKeys; i++ {
					tx.Get(strconv.Itoa(r.Intn(keys)))
				}
				if write {
					tx.Set(strconv.Itoa(r.Intn(keys)), 1)
				}
				tx.Commit()
				atomic.AddUint64(&ops, 1)
			}
		}(int64(w))
	}
	time.Sleep(duration)
	atomic.StoreInt32(&stop, 1)
	wg.Wait()
	return ops
}

func runLock(s *lockkv.Store) uint64 {
	var ops uint64
	var stop int32
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(seed int64) {
			defer wg.Done()
			r := rand.New(rand.NewSource(seed))
			for atomic.LoadInt32(&stop) == 0 {
				write := r.Intn(100) < writeRatio
				tx := s.Begin(write)
				for i := 0; i < txnKeys; i++ {
					tx.Get(strconv.Itoa(r.Intn(keys)))
				}
				if write {
					tx.Set(strconv.Itoa(r.Intn(keys)), 1)
				}
				tx.Commit()
				atomic.AddUint64(&ops, 1)
			}
		}(int64(w))
	}
	time.Sleep(duration)
	atomic.StoreInt32(&stop, 1)
	wg.Wait()
	return ops
}

func main() {
	e := occ.New()
	seed := e.Begin()
	for i := 0; i < keys; i++ {
		seed.Set(strconv.Itoa(i), i)
	}
	seed.Commit()

	lk := lockkv.New()
	ltx := lk.Begin(true)
	for i := 0; i < keys; i++ {
		ltx.Set(strconv.Itoa(i), i)
	}
	ltx.Commit()

	fmt.Printf("workload: %d workers, %d keys, %d reads/txn, %d%% write txns, %v each\n\n",
		workers, keys, txnKeys, writeRatio, duration)

	occOps := runOCC(e)
	commits, conflicts := e.Stats()
	fmt.Printf("OCC : %d txns (%.0f txns/s), commits=%d conflicts=%d conflict-rate=%.2f%%\n",
		occOps, float64(occOps)/duration.Seconds(), commits, conflicts, e.ConflictRate()*100)

	lockOps := runLock(lk)
	fmt.Printf("LOCK: %d txns (%.0f txns/s)\n", lockOps, float64(lockOps)/duration.Seconds())

	fmt.Printf("\nOCC speedup: %.2fx\n", float64(occOps)/float64(lockOps))
}
