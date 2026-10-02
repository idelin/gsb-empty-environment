package mvcc

import (
	"errors"
	"fmt"
	"sync"
	"testing"
)

// mustGet 读取并断言值与期望一致。
func mustGet(t *testing.T, txn *Txn, key, want string) {
	t.Helper()
	got, err := txn.Get(key)
	if err != nil {
		t.Fatalf("Get(%q) 出错: %v", key, err)
	}
	if string(got) != want {
		t.Fatalf("Get(%q) = %q, 期望 %q", key, got, want)
	}
}

// TestAcceptanceScenario 完整复现验收边界：
//   - T1(ID=10) 修改 K1 未提交；
//   - T2(ID=11) 读 K1 只能看到旧版本；
//   - T2 试图写 K1 触发写写冲突被 Abort；
//   - T1 提交后，新启动的 T3(ID=12) 成功读到 T1 写入的最新值。
func TestAcceptanceScenario(t *testing.T) {
	e := NewEngine()

	// 消耗事务 ID 1..8，再用 ID=9 的事务写入初始值 K1="v0" 并提交，
	// 使验收场景中的事务 ID 精确为 10、11、12。
	for i := 0; i < 8; i++ {
		if err := e.Begin().Abort(); err != nil {
			t.Fatalf("Abort 出错: %v", err)
		}
	}
	seed := e.Begin()
	if seed.ID() != 9 {
		t.Fatalf("seed 事务 ID = %d, 期望 9", seed.ID())
	}
	if err := seed.Put("K1", []byte("v0")); err != nil {
		t.Fatalf("seed Put 出错: %v", err)
	}
	if err := seed.Commit(); err != nil {
		t.Fatalf("seed Commit 出错: %v", err)
	}

	// T1(ID=10) 修改 K1，暂不提交。
	t1 := e.Begin()
	if t1.ID() != 10 {
		t.Fatalf("T1 ID = %d, 期望 10", t1.ID())
	}
	if err := t1.Put("K1", []byte("v1")); err != nil {
		t.Fatalf("T1 Put 出错: %v", err)
	}

	// T2(ID=11) 读 K1 只能看到旧版本 "v0"（快照隔离：看不到 T1 的未提交写入）。
	t2 := e.Begin()
	if t2.ID() != 11 {
		t.Fatalf("T2 ID = %d, 期望 11", t2.ID())
	}
	mustGet(t, t2, "K1", "v0")

	// T2 试图写 K1，触发写写冲突，被 Abort（首写获胜：T1 先写）。
	err := t2.Put("K1", []byte("v2"))
	if !errors.Is(err, ErrTxnAborted) {
		t.Fatalf("T2 Put 应触发写写冲突并 Abort, 实际 err = %v", err)
	}
	// 已中止的事务后续操作全部失败。
	if _, err := t2.Get("K1"); !errors.Is(err, ErrTxnAborted) {
		t.Fatalf("T2 中止后 Get 应返回 ErrTxnAborted, 实际 err = %v", err)
	}
	if err := t2.Commit(); !errors.Is(err, ErrTxnAborted) {
		t.Fatalf("T2 中止后 Commit 应返回 ErrTxnAborted, 实际 err = %v", err)
	}

	// T1 提交成功。
	if err := t1.Commit(); err != nil {
		t.Fatalf("T1 Commit 出错: %v", err)
	}

	// 新启动的 T3(ID=12) 读到 T1 写入的最新值 "v1"。
	t3 := e.Begin()
	if t3.ID() != 12 {
		t.Fatalf("T3 ID = %d, 期望 12", t3.ID())
	}
	mustGet(t, t3, "K1", "v1")
}

// TestSnapshotIsolation 验证快照隔离：事务开启后其他事务的提交对其不可见。
func TestSnapshotIsolation(t *testing.T) {
	e := NewEngine()

	seed := e.Begin()
	if err := seed.Put("K1", []byte("v0")); err != nil {
		t.Fatal(err)
	}
	if err := seed.Commit(); err != nil {
		t.Fatal(err)
	}

	reader := e.Begin() // 快照固定在此刻
	writer := e.Begin()
	if err := writer.Put("K1", []byte("v1")); err != nil {
		t.Fatal(err)
	}
	if err := writer.Commit(); err != nil {
		t.Fatal(err)
	}

	// writer 已提交，但 reader 的快照在提交之前，仍只能读到旧值。
	mustGet(t, reader, "K1", "v0")

	// 新事务可以看到已提交的新值。
	fresh := e.Begin()
	mustGet(t, fresh, "K1", "v1")
}

// TestReadYourWrites 验证事务能读到自己的未提交写入。
func TestReadYourWrites(t *testing.T) {
	e := NewEngine()
	txn := e.Begin()
	if err := txn.Put("K1", []byte("mine")); err != nil {
		t.Fatal(err)
	}
	mustGet(t, txn, "K1", "mine")
}

// TestFirstCommitterWinsAtCommit 验证提交时的首写获胜：
// 两个事务基于同一快照写同一个键（错开写入时间以避开 pending 冲突），
// 先提交者获胜，后提交者被中止。
func TestFirstCommitterWinsAtCommit(t *testing.T) {
	e := NewEngine()

	seed := e.Begin()
	if err := seed.Put("K1", []byte("v0")); err != nil {
		t.Fatal(err)
	}
	if err := seed.Commit(); err != nil {
		t.Fatal(err)
	}

	t1 := e.Begin()
	t2 := e.Begin()

	// t1 写入并先提交。
	if err := t1.Put("K1", []byte("v1")); err != nil {
		t.Fatal(err)
	}
	if err := t1.Commit(); err != nil {
		t.Fatalf("t1 应先提交成功: %v", err)
	}

	// t2 的快照早于 t1 的提交，写同一键时在提交点被首写获胜规则中止。
	if err := t2.Put("K1", []byte("v2")); err != nil {
		t.Fatal(err)
	}
	if err := t2.Commit(); !errors.Is(err, ErrTxnAborted) {
		t.Fatalf("t2 Commit 应被首写获胜规则中止, 实际 err = %v", err)
	}

	// 最终生效的是先提交者 t1 的值。
	check := e.Begin()
	mustGet(t, check, "K1", "v1")
}

// TestNonConflictingWrites 验证写不同键的事务互不影响，均可提交。
func TestNonConflictingWrites(t *testing.T) {
	e := NewEngine()

	t1 := e.Begin()
	t2 := e.Begin()
	if err := t1.Put("A", []byte("1")); err != nil {
		t.Fatal(err)
	}
	if err := t2.Put("B", []byte("2")); err != nil {
		t.Fatal(err)
	}
	if err := t1.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := t2.Commit(); err != nil {
		t.Fatal(err)
	}

	check := e.Begin()
	mustGet(t, check, "A", "1")
	mustGet(t, check, "B", "2")
}

// TestDelete 验证删除（tombstone）语义。
func TestDelete(t *testing.T) {
	e := NewEngine()

	seed := e.Begin()
	if err := seed.Put("K1", []byte("v0")); err != nil {
		t.Fatal(err)
	}
	if err := seed.Commit(); err != nil {
		t.Fatal(err)
	}

	del := e.Begin()
	if err := del.Delete("K1"); err != nil {
		t.Fatal(err)
	}
	if err := del.Commit(); err != nil {
		t.Fatal(err)
	}

	check := e.Begin()
	if _, err := check.Get("K1"); !errors.Is(err, ErrKeyNotFound) {
		t.Fatalf("删除后 Get 应返回 ErrKeyNotFound, 实际 err = %v", err)
	}
}

// TestAbortReleasesPending 验证事务中止后释放未提交写入，其他事务可继续写同一键。
func TestAbortReleasesPending(t *testing.T) {
	e := NewEngine()

	t1 := e.Begin()
	if err := t1.Put("K1", []byte("v1")); err != nil {
		t.Fatal(err)
	}
	if err := t1.Abort(); err != nil {
		t.Fatal(err)
	}

	t2 := e.Begin()
	if err := t2.Put("K1", []byte("v2")); err != nil {
		t.Fatalf("t1 中止后 t2 写同一键不应冲突: %v", err)
	}
	if err := t2.Commit(); err != nil {
		t.Fatal(err)
	}

	check := e.Begin()
	mustGet(t, check, "K1", "v2")
}

// TestMonotonicTxnIDs 验证事务 ID 单调自增。
func TestMonotonicTxnIDs(t *testing.T) {
	e := NewEngine()
	for want := uint64(1); want <= 100; want++ {
		if id := e.Begin().ID(); id != want {
			t.Fatalf("事务 ID = %d, 期望 %d", id, want)
		}
	}
}

// TestConcurrentTransactions 并发冒烟测试：多 goroutine 并发读写，
// 验证引擎在并发下无数据竞争、无 panic（配合 go test -race 运行）。
func TestConcurrentTransactions(t *testing.T) {
	e := NewEngine()
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			key := fmt.Sprintf("key-%d", g%4)
			for i := 0; i < 50; i++ {
				txn := e.Begin()
				_, _ = txn.Get(key)
				if err := txn.Put(key, []byte(fmt.Sprintf("g%d-i%d", g, i))); err != nil {
					continue // 写写冲突被中止，属预期行为
				}
				_ = txn.Commit()
			}
		}(g)
	}
	wg.Wait()
}
