package occ

import (
	"fmt"
	"sync"
	"testing"
)

// TestBasicCommitRead 验证基本的写入-提交-读取闭环。
func TestBasicCommitRead(t *testing.T) {
	e := New()
	tx := e.Begin()
	tx.Set("a", 1)
	tx.Set("b", 2)
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit failed: %v", err)
	}

	rx := e.Begin()
	if v, ok := rx.Get("a"); !ok || v.(int) != 1 {
		t.Fatalf("get a = %v,%v want 1,true", v, ok)
	}
	if v, ok := rx.Get("b"); !ok || v.(int) != 2 {
		t.Fatalf("get b = %v,%v want 2,true", v, ok)
	}
	if _, ok := rx.Get("missing"); ok {
		t.Fatal("missing key should not exist")
	}
	if err := rx.Commit(); err != nil {
		t.Fatalf("read-only commit failed: %v", err)
	}
}

// TestReadYourWrites 验证事务内能读到自己的未提交写入。
func TestReadYourWrites(t *testing.T) {
	e := New()
	tx := e.Begin()
	tx.Set("k", "v")
	if v, ok := tx.Get("k"); !ok || v.(string) != "v" {
		t.Fatalf("read-your-writes broken: %v,%v", v, ok)
	}
	// 未提交前对其他事务不可见
	other := e.Begin()
	if _, ok := other.Get("k"); ok {
		t.Fatal("uncommitted write leaked to another txn")
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	after := e.Begin()
	if v, ok := after.Get("k"); !ok || v.(string) != "v" {
		t.Fatalf("committed write not visible: %v,%v", v, ok)
	}
}

// TestStaleReadSetDetected 精准验证：ReadSet 版本过期 -> ErrConflict，
// 且冲突事务的写入绝不落入存储（干净回滚）。
func TestStaleReadSetDetected(t *testing.T) {
	e := New()
	init := e.Begin()
	init.Set("hot", 0)
	if err := init.Commit(); err != nil {
		t.Fatal(err)
	}

	// T1 读取 hot（记录版本 v1）
	t1 := e.Begin()
	if _, ok := t1.Get("hot"); !ok {
		t.Fatal("hot should exist")
	}
	t1.Set("t1-data", "should-never-appear")

	// T2 修改 hot 并先提交（推进版本到 v2）
	t2 := e.Begin()
	if _, ok := t2.Get("hot"); !ok {
		t.Fatal("hot should exist")
	}
	t2.Set("hot", 100)
	if err := t2.Commit(); err != nil {
		t.Fatal(err)
	}

	// T1 提交：ReadSet 中 hot 的版本已过期，必须冲突
	if err := t1.Commit(); err != ErrConflict {
		t.Fatalf("want ErrConflict, got %v", err)
	}

	// 干净回滚：T1 的写入不可见，T2 的写入完好
	check := e.Begin()
	if _, ok := check.Get("t1-data"); ok {
		t.Fatal("rolled-back write leaked into store")
	}
	if v, ok := check.Get("hot"); !ok || v.(int) != 100 {
		t.Fatalf("hot = %v,%v want 100,true", v, ok)
	}
	if err := check.Commit(); err != nil {
		t.Fatal(err)
	}

	_, conflicts := e.Stats()
	if conflicts != 1 {
		t.Fatalf("conflicts = %d, want 1", conflicts)
	}
}

// TestOverlapWriteConflict 两个事务重叠写同一热点键，后到者必须失败。
func TestOverlapWriteConflict(t *testing.T) {
	e := New()
	seed := e.Begin()
	seed.Set("x", 0)
	if err := seed.Commit(); err != nil {
		t.Fatal(err)
	}

	ta := e.Begin()
	tb := e.Begin()
	ta.Get("x")
	tb.Get("x")
	ta.Set("x", 1)
	tb.Set("x", 2)

	if err := ta.Commit(); err != nil {
		t.Fatalf("first committer should win: %v", err)
	}
	if err := tb.Commit(); err != ErrConflict {
		t.Fatalf("second committer must conflict, got %v", err)
	}

	v, _ := func() (interface{}, bool) { rx := e.Begin(); defer rx.Commit(); return rx.Get("x") }()
	if v.(int) != 1 {
		t.Fatalf("x = %v, want 1 (first committer wins)", v)
	}
}

// TestConcurrentHotKeys 高并发对少量热点键读写：
// 用"冲突即重试"的循环完成固定总量的增量，最终值必须精确等于增量总数，
// 证明冲突检测既无丢失更新也无幻提交。
func TestConcurrentHotKeys(t *testing.T) {
	e := New()
	seed := e.Begin()
	const keys = 8
	for i := 0; i < keys; i++ {
		seed.Set(fmt.Sprintf("k%d", i), 0)
	}
	if err := seed.Commit(); err != nil {
		t.Fatal(err)
	}

	const workers = 32
	const incrsPerWorker = 200
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			key := fmt.Sprintf("k%d", w%keys)
			for i := 0; i < incrsPerWorker; i++ {
				for { // 冲突重试循环
					tx := e.Begin()
					v, _ := tx.Get(key)
					tx.Set(key, v.(int)+1)
					if err := tx.Commit(); err == nil {
						break
					} else if err != ErrConflict {
						t.Errorf("unexpected error: %v", err)
						return
					}
				}
			}
		}(w)
	}
	wg.Wait()

	// 校验总和：每个 worker 恰好转账 incrsPerWorker 次 +1
	total := 0
	for i := 0; i < keys; i++ {
		rx := e.Begin()
		v, ok := rx.Get(fmt.Sprintf("k%d", i))
		if !ok {
			t.Fatalf("k%d missing", i)
		}
		total += v.(int)
		rx.Commit()
	}
	want := workers * incrsPerWorker
	if total != want {
		t.Fatalf("total = %d, want %d (lost update detected)", total, want)
	}

	commits, conflicts := e.Stats()
	t.Logf("hot-key workload: commits=%d conflicts=%d conflict-rate=%.2f%%",
		commits, conflicts, e.ConflictRate()*100)
	if conflicts == 0 {
		t.Fatal("expected conflicts on hot keys, got none")
	}
}

// TestReadOnlyTxnSeqlock 纯读事务在并发写下要么一致提交、要么干净冲突，
// 绝不观察到撕裂状态（a+b 恒等于总量）。
func TestReadOnlyTxnSeqlock(t *testing.T) {
	e := New()
	seed := e.Begin()
	seed.Set("a", 50)
	seed.Set("b", 50)
	if err := seed.Commit(); err != nil {
		t.Fatal(err)
	}

	stop := make(chan struct{})
	var writers, readers sync.WaitGroup

	// 写者：a->b 转账，保持 a+b 不变
	for w := 0; w < 4; w++ {
		writers.Add(1)
		go func() {
			defer writers.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				tx := e.Begin()
				a, _ := tx.Get("a")
				b, _ := tx.Get("b")
				tx.Set("a", a.(int)-1)
				tx.Set("b", b.(int)+1)
				tx.Commit() // 冲突则丢弃，无所谓
			}
		}()
	}

	// 读者：纯读事务观察到的一致性快照必须满足 a+b==100
	for r := 0; r < 8; r++ {
		readers.Add(1)
		go func() {
			defer readers.Done()
			for i := 0; i < 5000; i++ {
				tx := e.Begin()
				a, _ := tx.Get("a")
				b, _ := tx.Get("b")
				if err := tx.Commit(); err == ErrConflict {
					continue
				}
				if a.(int)+b.(int) != 100 {
					t.Errorf("torn read: a=%v b=%v", a, b)
					return
				}
			}
		}()
	}
	readers.Wait()
	close(stop)
	writers.Wait()
	commits, conflicts := e.Stats()
	t.Logf("seqlock workload: commits=%d conflicts=%d", commits, conflicts)
}

// TestRollbackDiscardsWrites 显式回滚后写入不可见。
func TestRollbackDiscardsWrites(t *testing.T) {
	e := New()
	tx := e.Begin()
	tx.Set("gone", 1)
	tx.Rollback()
	rx := e.Begin()
	if _, ok := rx.Get("gone"); ok {
		t.Fatal("rolled-back write visible")
	}
}
