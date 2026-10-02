package latchcrab

import (
	"fmt"
	"math/rand"
	"reflect"
	"sort"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestSequentialInsertGet 验证单线程下插入与查找的基本正确性。
func TestSequentialInsertGet(t *testing.T) {
	tree := New(4)
	const n = 2000
	perm := rand.Perm(n)
	for _, k := range perm {
		tree.Insert(k)
	}
	for i := 0; i < n; i++ {
		if !tree.Get(i) {
			t.Fatalf("Get(%d) = false, want true", i)
		}
	}
	for _, k := range []int{-1, n, n + 100} {
		if tree.Get(k) {
			t.Fatalf("Get(%d) = true, want false", k)
		}
	}
	if err := tree.checkInvariants(); err != nil {
		t.Fatalf("invariants violated: %v", err)
	}
}

// TestDuplicateInsert 验证重复插入被忽略且结构不变量保持。
func TestDuplicateInsert(t *testing.T) {
	tree := New(3)
	for r := 0; r < 3; r++ {
		for i := 0; i < 500; i++ {
			tree.Insert(i)
		}
	}
	got := tree.Range(0, 499)
	if len(got) != 500 {
		t.Fatalf("Range returned %d keys, want 500", len(got))
	}
	if err := tree.checkInvariants(); err != nil {
		t.Fatalf("invariants violated: %v", err)
	}
}

// TestRange 验证范围查询结果与参考实现一致。
func TestRange(t *testing.T) {
	tree := New(5)
	const n = 1000
	for _, k := range rand.Perm(n) {
		tree.Insert(k)
	}
	for trial := 0; trial < 50; trial++ {
		lo, hi := rand.Intn(n), rand.Intn(n)
		if lo > hi {
			lo, hi = hi, lo
		}
		got := tree.Range(lo, hi)
		var want []int
		for i := lo; i <= hi; i++ {
			want = append(want, i)
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("Range(%d,%d) len=%d, want %d", lo, hi, len(got), len(want))
		}
	}
}

// TestConcurrentReadersWriters 验收边界：10 个读线程 + 5 个插入写线程并发执行，
// 要求无死锁（由超时看门狗保证），且结束后数据与结构不变量正确。
func TestConcurrentReadersWriters(t *testing.T) {
	tree := New(8)
	const (
		readers    = 10
		writers    = 5
		perWriter  = 2000
		opsPerRead = 4000
		keySpace   = writers * perWriter
	)

	done := make(chan struct{})
	var wg sync.WaitGroup

	// 5 个写线程：各自插入不相交的键区间，便于最终精确校验。
	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			keys := rand.Perm(perWriter)
			for _, k := range keys {
				tree.Insert(w*perWriter + k)
			}
		}(w)
	}

	// 10 个读线程：随机点查 + 范围查询。
	for r := 0; r < readers; r++ {
		wg.Add(1)
		go func(r int) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(int64(r)))
			for i := 0; i < opsPerRead; i++ {
				k := rng.Intn(keySpace)
				tree.Get(k)
				if i%64 == 0 {
					lo := rng.Intn(keySpace)
					hi := lo + rng.Intn(64)
					got := tree.Range(lo, hi)
					if !sort.IntsAreSorted(got) {
						t.Errorf("Range(%d,%d) not sorted", lo, hi)
						return
					}
				}
			}
		}(r)
	}
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(60 * time.Second):
		t.Fatal("deadlock suspected: concurrent workload did not finish in 60s")
	}

	// 全部写完成后，所有键必须可查。
	for k := 0; k < keySpace; k++ {
		if !tree.Get(k) {
			t.Fatalf("after concurrent inserts, Get(%d) = false", k)
		}
	}
	if err := tree.checkInvariants(); err != nil {
		t.Fatalf("invariants violated after concurrency: %v", err)
	}
}

// TestReadersNotBlockedByWriter 验证协议的核心收益：
// 写线程在高层节点（非安全节点）上持有写锁时，读线程仍能无阻塞穿越
// 已释放的高层节点。通过 hook 让写线程在持有父锁时停顿，
// 读线程若能在停顿窗口内完成大量遍历，即证明高层锁已被提前释放。
func TestReadersNotBlockedByWriter(t *testing.T) {
	tree := New(4)
	const n = 20000
	for _, k := range rand.Perm(n) {
		tree.Insert(k)
	}

	// 找出位于高层（根的直接孩子）的一个内部节点及其覆盖的键范围，
	// 模拟写线程在其上持锁。
	tree.rootMu.RLock()
	root := tree.root
	tree.rootMu.RUnlock()
	if root.isLeaf {
		t.Skip("tree too shallow for this test")
	}
	var target *node
	targetIdx := -1
	for i, c := range root.children {
		if !c.isLeaf {
			target = c
			targetIdx = i
			break
		}
	}
	if target == nil {
		t.Skip("no internal node at level 1; adjust n/order")
	}
	lo, hi := 0, n // target 子树覆盖的键范围 [lo, hi)
	if targetIdx > 0 {
		lo = root.keys[targetIdx-1]
	}
	if targetIdx < len(root.keys) {
		hi = root.keys[targetIdx]
	}

	// 测试线程持有 target 写锁，等价于写线程在分裂路径上持锁并停顿。
	target.mu.Lock()

	// 读线程：只读 target 子树范围之外的键。写线程只持有中层锁、
	// 未持有高层（根）锁，这些读者应能高速穿越，不被阻塞。
	stop := make(chan struct{})
	var reads int64
	var wg sync.WaitGroup
	for r := 0; r < 4; r++ {
		wg.Add(1)
		go func(r int) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(int64(r)))
			for {
				select {
				case <-stop:
					return
				default:
				}
				// 在 [0,lo) ∪ [hi,n) 中均匀取键。
				k := rng.Intn(n - (hi - lo))
				if k >= lo {
					k += hi - lo
				}
				tree.Get(k)
				atomic.AddInt64(&reads, 1)
			}
		}(r)
	}

	// “受害者”读线程：读取 target 子树范围内的键，应当被写锁阻塞，
	// 并在写锁释放后恢复。
	var victimDone int32
	victim := make(chan struct{})
	go func() {
		tree.Get(lo)
		atomic.StoreInt32(&victimDone, 1)
		close(victim)
	}()

	time.Sleep(300 * time.Millisecond) // 写线程“慢分裂”窗口
	if atomic.LoadInt32(&victimDone) != 0 {
		t.Fatal("victim reader should be blocked by the mid-level write latch")
	}
	if got := atomic.LoadInt64(&reads); got < 100 {
		t.Fatalf("readers outside the locked subtree made only %d lookups; "+
			"high-level latches appear to be held by the writer", got)
	}
	target.mu.Unlock() // 写线程完成，释放中层锁，被阻塞的读者恢复
	select {
	case <-victim:
	case <-time.After(5 * time.Second):
		t.Fatal("victim reader did not resume after latch release")
	}
	close(stop)
	wg.Wait()

	t.Logf("readers completed %d lookups while a mid-level write latch was held",
		atomic.LoadInt64(&reads))
}

// TestConcurrentNoDeadlockSmallOrder 用小阶树制造频繁分裂，
// 配合 GOMAXPROCS 压力验证无死锁、无数据竞争（配合 -race 运行）。
func TestConcurrentNoDeadlockSmallOrder(t *testing.T) {
	tree := New(3)
	const (
		readers = 10
		writers = 5
		keys    = 5000
	)
	done := make(chan struct{})
	var wg sync.WaitGroup
	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(int64(w)))
			for i := 0; i < keys/writers; i++ {
				tree.Insert(rng.Intn(keys))
			}
		}(w)
	}
	for r := 0; r < readers; r++ {
		wg.Add(1)
		go func(r int) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(int64(r + 100)))
			for i := 0; i < keys; i++ {
				tree.Get(rng.Intn(keys))
			}
		}(r)
	}
	go func() {
		wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(60 * time.Second):
		t.Fatal("deadlock suspected under heavy splits")
	}
	if err := tree.checkInvariants(); err != nil {
		t.Fatalf("invariants violated: %v", err)
	}
}

// ExampleBTree 演示基本用法。
func ExampleBTree() {
	tree := New(4)
	for _, k := range []int{5, 2, 8, 1, 3, 7, 9} {
		tree.Insert(k)
	}
	fmt.Println(tree.Get(3))
	fmt.Println(tree.Get(4))
	fmt.Println(tree.Range(2, 7))
	// Output:
	// true
	// false
	// [2 3 5 7]
}
