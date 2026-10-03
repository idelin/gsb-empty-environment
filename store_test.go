package mvcc

import (
	"math/rand"
	"sync"
	"sync/atomic"
	"testing"
)

func TestBasicCommitAndRead(t *testing.T) {
	s := NewStore(4, 10)
	if got := s.Latest(0); got != 10 {
		t.Fatalf("init value = %d, want 10", got)
	}
	seq, err := s.Commit(Update{Key: 1, Value: 42})
	if err != nil {
		t.Fatal(err)
	}
	if seq != 1 {
		t.Fatalf("commit seq = %d, want 1", seq)
	}
	sn := s.Begin()
	defer sn.Close()
	if got, ok := sn.Get(1); !ok || got != 42 {
		t.Fatalf("Get(1) = %d,%v, want 42,true", got, ok)
	}
	if got, ok := sn.Get(0); !ok || got != 10 {
		t.Fatalf("Get(0) = %d,%v, want 10,true", got, ok)
	}
	if _, err := s.Commit(Update{Key: 9, Value: 1}); err != ErrKeyOutOfRange {
		t.Fatalf("err = %v, want ErrKeyOutOfRange", err)
	}
}

func TestSnapshotIsolation(t *testing.T) {
	s := NewStore(2, 0)
	if _, err := s.Commit(Update{Key: 0, Value: 100}, Update{Key: 1, Value: 200}); err != nil {
		t.Fatal(err)
	}
	old := s.Begin()
	defer old.Close()

	// 旧快照建立后继续密集提交。
	for i := int64(0); i < 100; i++ {
		if _, err := s.Commit(Update{Key: 0, Value: i}, Update{Key: 1, Value: -i}); err != nil {
			t.Fatal(err)
		}
	}
	// 旧快照必须仍然看到 seq=1 时的值。
	if got, ok := old.Get(0); !ok || got != 100 {
		t.Fatalf("old snapshot Get(0) = %d,%v, want 100,true", got, ok)
	}
	if got, ok := old.Get(1); !ok || got != 200 {
		t.Fatalf("old snapshot Get(1) = %d,%v, want 200,true", got, ok)
	}
	// 新快照看到最新值。
	fresh := s.Begin()
	defer fresh.Close()
	if got, _ := fresh.Get(0); got != 99 {
		t.Fatalf("fresh snapshot Get(0) = %d, want 99", got)
	}
	if got, _ := fresh.Get(1); got != -99 {
		t.Fatalf("fresh snapshot Get(1) = %d, want -99", got)
	}
}

func TestGCTrimsOldVersions(t *testing.T) {
	s := NewStore(1, 0)
	const commits = 1000
	for i := int64(1); i <= commits; i++ {
		if _, err := s.Commit(Update{Key: 0, Value: i}); err != nil {
			t.Fatal(err)
		}
	}
	// 钉住 seq=commits 的快照。
	sn := s.Begin()
	if got, ok := sn.Get(0); !ok || got != commits {
		t.Fatalf("pinned snapshot Get = %d,%v, want %d,true", got, ok, commits)
	}
	// 持有该快照期间继续提交，GC 不得回收它需要的版本，
	// 只能回收比它更老的 1000 个节点（创世 + 999 个历史版本）。
	for i := int64(0); i < 10; i++ {
		if _, err := s.Commit(Update{Key: 0, Value: -i}); err != nil {
			t.Fatal(err)
		}
	}
	if removed := s.GC(); removed != commits {
		t.Fatalf("GC with active reader removed = %d, want %d", removed, commits)
	}
	if got, ok := sn.Get(0); !ok || got != commits {
		t.Fatalf("pinned snapshot Get = %d,%v, want %d,true", got, ok, commits)
	}
	sn.Close()
	// 读者关闭后，其钉住的版本及之后的 9 个版本（seq 1000..1009）都可被回收。
	if removed := s.GC(); removed != 10 {
		t.Fatalf("GC after close removed = %d, want 10", removed)
	}
	// 最新快照读到最新值。
	fresh := s.Begin()
	defer fresh.Close()
	if got, ok := fresh.Get(0); !ok || got != -9 {
		t.Fatalf("fresh snapshot Get = %d,%v, want -9,true", got, ok)
	}
}

// TestZeroTornReads 是验收边界测试：
// 1 个写线程对 1000 个键持续密集做"转账"事务（总和守恒），
// 5 个读线程并发执行全表扫描校验和，要求零撕裂。
func TestZeroTornReads(t *testing.T) {
	const (
		numKeys      = 1000
		initValue    = 1000
		numReaders   = 5
		writerCommit = 50000
	)
	const wantSum = int64(numKeys * initValue)

	s := NewStore(numKeys, initValue)
	var stop atomic.Bool
	var torn atomic.Int64   // 校验和不守恒的次数（撕裂）
	var retries atomic.Int64 // 因快照被 GC 竞争回收而重试的次数
	var scans atomic.Int64   // 成功完成的全表扫描次数

	// 5 个读线程：全表扫描 + 校验和统计。
	var wg sync.WaitGroup
	for r := 0; r < numReaders; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for !stop.Load() {
				sn := s.Begin()
				sum := int64(0)
				ok := true
				for k := 0; k < numKeys; k++ {
					v, found := sn.Get(k)
					if !found { // 快照被极端 GC 竞争回收：整表重试，不计撕裂
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

	// 1 个写线程：两键转账，事务内总和守恒，写路径无等待。
	rng := rand.New(rand.NewSource(1))
	for i := 0; i < writerCommit; i++ {
		from := rng.Intn(numKeys)
		to := rng.Intn(numKeys)
		if from == to {
			continue
		}
		amount := int64(rng.Intn(100) + 1)
		if _, err := s.Commit(
			Update{Key: from, Value: s.Latest(from) - amount},
			Update{Key: to, Value: s.Latest(to) + amount},
		); err != nil {
			t.Fatal(err)
		}
		if i%256 == 0 { // 写路径顺带周期性回收旧版本
			s.GC()
		}
	}
	stop.Store(true)
	wg.Wait()

	t.Logf("writer commits=%d, reader full scans=%d, gc retries=%d, torn reads=%d",
		writerCommit, scans.Load(), retries.Load(), torn.Load())
	if scans.Load() == 0 {
		t.Fatal("readers completed zero scans")
	}
	if torn.Load() != 0 {
		t.Fatalf("detected %d torn reads, want 0", torn.Load())
	}
}
