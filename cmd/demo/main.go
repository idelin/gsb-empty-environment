// demo 复现验收场景：1 个写线程密集更新 1000 个键，
// 5 个读线程并发全表扫描校验和，验证零撕裂。
package main

import (
	"fmt"
	"math/rand"
	"sync"
	"sync/atomic"
	"time"

	mvcc "github.com/example/mvcc"
)

const (
	numKeys    = 1000
	initValue  = 1000
	numReaders = 5
	duration   = 3 * time.Second
)

func main() {
	const wantSum = int64(numKeys * initValue)
	s := mvcc.NewStore(numKeys, initValue)

	var stop atomic.Bool
	var commits, scans, retries, torn atomic.Int64

	var wg sync.WaitGroup
	for r := 0; r < numReaders; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for !stop.Load() {
				sn := s.Begin()
				sum, ok := int64(0), true
				for k := 0; k < numKeys; k++ {
					v, found := sn.Get(k)
					if !found {
						ok = false
						break
					}
					sum += v
				}
				sn.Close()
				if !ok {
					retries.Add(1)
					continue
				}
				if sum != wantSum {
					torn.Add(1)
				}
				scans.Add(1)
			}
		}()
	}

	wg.Add(1)
	go func() {
		defer wg.Done()
		rng := rand.New(rand.NewSource(time.Now().UnixNano()))
		for !stop.Load() {
			from, to := rng.Intn(numKeys), rng.Intn(numKeys)
			if from == to {
				continue
			}
			amount := int64(rng.Intn(100) + 1)
			_, _ = s.Commit(
				mvcc.Update{Key: from, Value: s.Latest(from) - amount},
				mvcc.Update{Key: to, Value: s.Latest(to) + amount},
			)
			if n := commits.Add(1); n%512 == 0 {
				s.GC()
			}
		}
	}()

	time.Sleep(duration)
	stop.Store(true)
	wg.Wait()

	fmt.Printf("运行时长:        %v\n", duration)
	fmt.Printf("写事务提交数:    %d (%.0f txn/s)\n", commits.Load(), float64(commits.Load())/duration.Seconds())
	fmt.Printf("全表扫描次数:    %d (%.0f scan/s)\n", scans.Load(), float64(scans.Load())/duration.Seconds())
	fmt.Printf("GC 竞争重试:     %d\n", retries.Load())
	fmt.Printf("校验和期望值:    %d\n", wantSum)
	fmt.Printf("撕裂读次数:      %d\n", torn.Load())
	if torn.Load() == 0 {
		fmt.Println("结果: PASS —— 零撕裂，读路径全程无锁")
	} else {
		fmt.Println("结果: FAIL")
	}
}
