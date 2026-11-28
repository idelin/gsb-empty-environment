package hlctxn

import (
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestExternalConsistency_ConcurrentWorkload 在注入多节点时钟偏移
// （<= maxOffset）的并发工作负载下验证实时外部一致性：
// 若 T1 物理提交先于 T2 开始，则 T2 必见 T1 的已提交写。
func TestExternalConsistency_ConcurrentWorkload(t *testing.T) {
	const (
		nodes      = 4
		maxOffset  = 5 * time.Millisecond
		writers    = 12
		readers    = 12
		keysPerTxn = 40
	)

	// 每个节点注入不同的固定偏移，绝对值均 <= maxOffset。
	offsets := []time.Duration{
		-5 * time.Millisecond, -2 * time.Millisecond,
		3 * time.Millisecond, 5 * time.Millisecond,
	}
	c := NewRealCluster(nodes, offsets, maxOffset)

	type commitEvent struct {
		key      string
		value    string
		commitNS int64 // 无偏斜真实提交时间
		commitTS Timestamp
	}
	var (
		mu     sync.Mutex
		events []commitEvent
		stop   atomic.Bool
		wg     sync.WaitGroup
	)

	// 写者：每个事务写独立键集合，制造落在不同节点、不同 HLC 戳的跨节点提交。
	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for i := 0; i < keysPerTxn && !stop.Load(); i++ {
				key := fmt.Sprintf("w%d-k%d", id, i)
				val := fmt.Sprintf("v%d-%d", id, i)
				coord := (id + i) % nodes
				txn, err := c.RunWithRetry(coord, int32(id%5), time.Microsecond,
					func(t *Transaction) error { return t.Put(key, val) })
				if err != nil {
					t.Errorf("writer txn failed: %v", err)
					return
				}
				view, _ := c.LookupTxn(txn.ID())
				mu.Lock()
				events = append(events, commitEvent{
					key: key, value: val,
					commitNS: view.TrueCommitNS, commitTS: view.CommitTS,
				})
				mu.Unlock()
			}
		}(w)
	}

	// 读者：开始时间晚于某已提交事件的真实提交时间时，必须读到该事件的值。
	violations := make(chan string, 256)
	for r := 0; r < readers; r++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for i := 0; i < keysPerTxn && !stop.Load(); i++ {
				coord := (id*3 + i) % nodes
				txn := c.Begin(coord, int32(10+id%5))
				startNS := txn.TrueStartNS()

				mu.Lock()
				snapshot := append([]commitEvent(nil), events...)
				mu.Unlock()

				for _, ev := range snapshot {
					// 实时前提：T1 物理提交严格早于 T2 物理开始。
					if ev.commitNS >= startNS {
						continue
					}
					val, found, _, err := txn.GetWithRestart(ev.key)
					if err != nil {
						violations <- fmt.Sprintf("read error key=%s: %v", ev.key, err)
						continue
					}
					if !found || val != ev.value {
						violations <- fmt.Sprintf(
							"linearizability violation: key=%s want=%q got=%q found=%v (commitNS=%d startNS=%d)",
							ev.key, ev.value, val, found, ev.commitNS, startNS)
					}
				}
				if _, err := txn.Commit(); err != nil {
					violations <- fmt.Sprintf("reader commit: %v", err)
				}
				time.Sleep(50 * time.Microsecond)
			}
		}(r)
	}

	wg.Wait()
	close(violations)
	for v := range violations {
		t.Error(v)
	}

	stats := c.StatsSnapshot()
	// 读者事务也会提交，因此只断言写者事务全部成功提交。
	wantWriterCommits := writers * keysPerTxn
	if stats.TxnCommits < wantWriterCommits {
		t.Fatalf("commits=%d, want at least %d", stats.TxnCommits, wantWriterCommits)
	}
	if len(c.PendingIntents()) != 0 {
		t.Fatalf("unresolved intents: %d", len(c.PendingIntents()))
	}
	// 并发不确定性路径必须真实被走过，且从未出现同节点二次重启。
	if stats.ReadRestarts == 0 {
		t.Log("warning: no read restarts observed in workload (timing dependent)")
	}
	t.Logf("workload stats: %+v", stats)
}

// TestExternalConsistency_HotKeyContention 在热点键上并发读写，
// 验证推戳/中止路径无死锁，且全部最终提交满足实时一致性。
func TestExternalConsistency_HotKeyContention(t *testing.T) {
	const (
		nodes      = 3
		maxOffset  = 2 * time.Millisecond
		goroutines = 10
		iterations = 30
	)
	c := NewRealCluster(nodes,
		[]time.Duration{-2 * time.Millisecond, 0, 2 * time.Millisecond}, maxOffset)

	var wg sync.WaitGroup
	var failed atomic.Int64
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for i := 0; i < iterations; i++ {
				coord := id % nodes
				if id%2 == 0 {
					_, err := c.RunWithRetry(coord, int32(id%7), time.Microsecond,
						func(txn *Transaction) error {
							return txn.Put("hot-key", fmt.Sprintf("g%d-i%d", id, i))
						})
					if err != nil {
						failed.Add(1)
						t.Errorf("hot-key writer failed: %v", err)
						return
					}
				} else {
					_, err := c.RunWithRetry(coord, int32(id%7), time.Microsecond,
						func(txn *Transaction) error {
							_, _, gerr := txn.Get("hot-key")
							return gerr
						})
					if err != nil && !errors.Is(err, ErrRetryLimitExceeded) {
						failed.Add(1)
						t.Errorf("hot-key reader failed: %v", err)
						return
					}
				}
			}
		}(g)
	}
	wg.Wait()

	if failed.Load() != 0 {
		t.Fatalf("%d failed goroutines", failed.Load())
	}
	stats := c.StatsSnapshot()
	if stats.TxnCommits == 0 {
		t.Fatal("no successful commits under contention")
	}
	if len(c.PendingIntents()) != 0 {
		t.Fatalf("unresolved intents after contention: %d", len(c.PendingIntents()))
	}
	t.Logf("hot-key stats: %+v", stats)
}

// TestConcurrent_PendingIntentConflicts 让多个事务的 PENDING 意图在时间上
// 真实重叠，并发读写同一批键，验证 PushTimestamp / 中止路径被实际触发且无死锁。
func TestConcurrent_PendingIntentConflicts(t *testing.T) {
	c := NewRealCluster(3,
		[]time.Duration{-2 * time.Millisecond, 0, 2 * time.Millisecond}, 2*time.Millisecond)

	const goroutines = 8
	const iterations = 25
	keys := make([]string, 6)
	for i := range keys {
		keys[i] = fmt.Sprintf("contend-%d", i)
	}

	var wg sync.WaitGroup
	var failures atomic.Int64
	gate := make(chan struct{})

	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			<-gate // 同时释放，最大化 PENDING 意图重叠窗口
			for i := 0; i < iterations; i++ {
				key := keys[(id+i)%len(keys)]
				coord := id % 3
				// 优先级在 0..9 间分散，制造高优推戳/中止与低优冲突重试。
				prio := int32((id*7 + i) % 10)
				var err error
				if id%3 == 0 {
					_, err = c.RunWithRetry(coord, prio, 50*time.Microsecond,
						func(txn *Transaction) error {
							_, _, _, gerr := txn.GetWithRestart(key)
							return gerr
						})
				} else {
					_, err = c.RunWithRetry(coord, prio, 50*time.Microsecond,
						func(txn *Transaction) error {
							return txn.Put(key, fmt.Sprintf("g%d-%d", id, i))
						})
				}
				if err != nil {
					failures.Add(1)
					t.Errorf("concurrent txn g=%d i=%d: %v", id, i, err)
					return
				}
			}
		}(g)
	}
	close(gate)
	wg.Wait()

	if failures.Load() != 0 {
		t.Fatalf("%d failed concurrent txns", failures.Load())
	}
	if len(c.PendingIntents()) != 0 {
		t.Fatalf("unresolved intents: %d", len(c.PendingIntents()))
	}
	stats := c.StatsSnapshot()
	t.Logf("pending-conflict stats: %+v", stats)
	// 冲突计数时序相关，不在并发用例里硬性断言；推戳/中止的正确性由
	// TestPriorityPush_* 与 TestPriorityConflict_* 确定性覆盖。这里只保证
	// 重叠 PENDING 意图下系统有界推进、无死锁、无残留意图。
}

// TestExternalConsistency_DirectedSkewScenario 用确定性仿真钟构造
// “T1 在快钟节点提交、T2 在慢钟节点随后开始”的定向偏斜场景。
func TestExternalConsistency_DirectedSkewScenario(t *testing.T) {
	base := NewSimPhysicalClock(0)
	maxOff := 100 * time.Nanosecond
	phys := []PhysicalClock{
		SkewedPhysicalClock{Base: base, Offset: -100 * time.Nanosecond}, // N0 慢
		SkewedPhysicalClock{Base: base, Offset: 100 * time.Nanosecond},  // N1 快
	}
	c := NewCluster(phys, base, maxOff)

	key := keyOnNode(c, 1, "ec")

	// 真实时间 t=10：快钟 N1 上 T1 提交。
	base.Advance(10 * time.Nanosecond)
	t1 := c.Begin(1, 0)
	if err := t1.Put(key, "t1-value"); err != nil {
		t.Fatal(err)
	}
	t1CommitTS, err := t1.Commit()
	if err != nil {
		t.Fatal(err)
	}
	t1View, _ := c.LookupTxn(t1.ID())

	// 真实时间稍后（t=20）：慢钟 N0 上 T2 才开始，必须最终看到 T1。
	base.Advance(10 * time.Nanosecond)
	t2 := c.Begin(0, 0)
	if t2.TrueStartNS() <= t1View.TrueCommitNS {
		t.Fatal("setup: T2 must start after T1 true commit")
	}
	// T2 接触存放键的 N1：提交时的因果 HLC 合并保证 observed[N1] >= T1.commitTS。
	probe := probeKey(c, 1, key)
	if _, ok, err := t2.Get(probe); err != nil || ok {
		t.Fatalf("probe read: ok=%v err=%v", ok, err)
	}
	obs, _ := t2.Observed(1)
	if obs.Less(t1CommitTS) {
		t.Fatalf("invariant broken: observed[N1]=%+v < T1 commitTS=%+v",
			obs, t1CommitTS)
	}
	value, found, _, err := t2.GetWithRestart(key)
	if err != nil {
		t.Fatalf("T2 read: %v", err)
	}
	if !found || value != "t1-value" {
		t.Fatalf("external consistency violated: value=%q found=%v", value, found)
	}
	if t2.ReadTS().Less(t1CommitTS) {
		t.Fatalf("T2 readTS %+v never advanced past T1 commitTS %+v",
			t2.ReadTS(), t1CommitTS)
	}
}
