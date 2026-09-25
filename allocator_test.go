package pagealloc

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

var errInjected = errors.New("pagealloc: injected crash")

func openAt(t *testing.T, dir string, pages uint64, hooks *Hooks) *Allocator {
	t.Helper()
	a, err := Open(Options{Dir: dir, PageCount: pages, Hooks: hooks})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	return a
}

// allocAll 把剩余空闲页全部申请完，返回所有已占页号（含 previously）。
// 用于验证总页数守恒与无重复分配。
func drain(t *testing.T, a *Allocator, allocated map[uint64]bool) {
	t.Helper()
	for {
		tx := a.Begin()
		start, err := tx.Alloc(1)
		if errors.Is(err, ErrNoSpace) {
			tx.Rollback()
			return
		}
		if err != nil {
			t.Fatalf("drain alloc: %v", err)
		}
		if allocated[start] {
			t.Fatalf("page %d allocated twice", start)
		}
		allocated[start] = true
		if err := tx.Commit(); err != nil {
			t.Fatalf("drain commit: %v", err)
		}
	}
}

// TestRollbackReuse 先申请 3 页再回滚，之后可原样重用。
func TestRollbackReuse(t *testing.T) {
	a := openAt(t, t.TempDir(), 16, nil)

	tx := a.Begin()
	first, err := tx.Alloc(3)
	if err != nil {
		t.Fatalf("alloc: %v", err)
	}
	tx.Rollback()

	tx = a.Begin()
	second, err := tx.Alloc(3)
	if err != nil {
		t.Fatalf("alloc after rollback: %v", err)
	}
	if first != second {
		t.Fatalf("rolled-back pages not reusable: got start %d, want %d", second, first)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}
	if got, want := a.FreeCount(), uint64(13); got != want {
		t.Fatalf("free count = %d, want %d", got, want)
	}
}

// TestCoalesce 相邻空闲区释放后合并，可申请更大的连续区间。
func TestCoalesce(t *testing.T) {
	a := openAt(t, t.TempDir(), 16, nil)

	tx := a.Begin()
	for i := 0; i < 3; i++ {
		if _, err := tx.Alloc(2); err != nil {
			t.Fatalf("alloc: %v", err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}

	// 交错释放 0..5，触发前后两个方向的合并
	tx = a.Begin()
	for _, id := range []uint64{2, 4, 0, 3, 5, 1} {
		if err := tx.Free(id); err != nil {
			t.Fatalf("free %d: %v", id, err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}

	// 合并后前 6 页连续空闲，可一次性申请
	tx = a.Begin()
	start, err := tx.Alloc(6)
	if err != nil {
		t.Fatalf("alloc coalesced range: %v", err)
	}
	if start != 0 {
		t.Fatalf("coalesced alloc start = %d, want 0", start)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}
}

// TestNoReallocate 已占用页不会被重复分配；耗尽后报 ErrNoSpace。
func TestNoReallocate(t *testing.T) {
	a := openAt(t, t.TempDir(), 8, nil)
	seen := map[uint64]bool{}

	tx := a.Begin()
	s1, _ := tx.Alloc(3)
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}
	for i := uint64(0); i < 3; i++ {
		seen[s1+i] = true
	}

	drain(t, a, seen)
	if len(seen) != 8 {
		t.Fatalf("allocated %d distinct pages, want 8", len(seen))
	}

	tx = a.Begin()
	if _, err := tx.Alloc(1); !errors.Is(err, ErrNoSpace) {
		t.Fatalf("alloc on full allocator = %v, want ErrNoSpace", err)
	}
	tx.Rollback()
}

// TestDoubleFree 事务内与跨事务的双重释放都必须拒绝。
func TestDoubleFree(t *testing.T) {
	a := openAt(t, t.TempDir(), 8, nil)

	tx := a.Begin()
	if _, err := tx.Alloc(2); err != nil {
		t.Fatalf("alloc: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}

	// 同一事务内重复释放
	tx = a.Begin()
	if err := tx.Free(0); err != nil {
		t.Fatalf("first free: %v", err)
	}
	if err := tx.Free(0); !errors.Is(err, ErrDoubleFree) {
		t.Fatalf("second free in txn = %v, want ErrDoubleFree", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}

	// 跨事务重复释放
	tx = a.Begin()
	if err := tx.Free(0); !errors.Is(err, ErrDoubleFree) {
		t.Fatalf("free after committed free = %v, want ErrDoubleFree", err)
	}
	tx.Rollback()

	// 释放从未分配过的页（一直空闲）同样是双重释放
	tx = a.Begin()
	if err := tx.Free(7); !errors.Is(err, ErrDoubleFree) {
		t.Fatalf("free of never-allocated page = %v, want ErrDoubleFree", err)
	}
	tx.Rollback()
}

// TestOutOfRangeFree 跨范围释放必须拒绝。
func TestOutOfRangeFree(t *testing.T) {
	a := openAt(t, t.TempDir(), 8, nil)
	tx := a.Begin()
	for _, id := range []uint64{8, 100, 1 << 40} {
		if err := tx.Free(id); !errors.Is(err, ErrOutOfRange) {
			t.Fatalf("Free(%d) = %v, want ErrOutOfRange", id, err)
		}
	}
	// 范围尾部越界
	if err := tx.FreeRange(6, 3); !errors.Is(err, ErrOutOfRange) {
		t.Fatalf("FreeRange(6,3) = %v, want ErrOutOfRange", err)
	}
	tx.Rollback()
}

// TestFreeRange 连续区间整体释放；部分已空闲时整体拒绝。
func TestFreeRange(t *testing.T) {
	a := openAt(t, t.TempDir(), 16, nil)
	tx := a.Begin()
	start, _ := tx.Alloc(4) // 0..3
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}

	tx = a.Begin()
	if err := tx.FreeRange(start, 4); err != nil {
		t.Fatalf("FreeRange: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}
	if got := a.FreeCount(); got != 16 {
		t.Fatalf("free count = %d, want 16", got)
	}

	// 范围 [2,6) 中 2..3 已空闲，整体拒绝
	tx = a.Begin()
	if _, err := tx.Alloc(2); err != nil { // 占用 0..1
		t.Fatalf("alloc: %v", err)
	}
	if err := tx.FreeRange(0, 4); !errors.Is(err, ErrDoubleFree) {
		t.Fatalf("FreeRange overlapping free pages = %v, want ErrDoubleFree", err)
	}
	tx.Rollback()
}

// TestPersistence 提交后的状态在重新打开后保持。
func TestPersistence(t *testing.T) {
	dir := t.TempDir()
	a := openAt(t, dir, 16, nil)
	tx := a.Begin()
	if _, err := tx.Alloc(5); err != nil {
		t.Fatalf("alloc: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}

	a2 := openAt(t, dir, 16, nil)
	if got := a2.FreeCount(); got != 11 {
		t.Fatalf("free count after reopen = %d, want 11", got)
	}
	// 已提交的 0..4 不可再分配，下一段从 5 开始
	tx = a2.Begin()
	start, err := tx.Alloc(5)
	if err != nil {
		t.Fatalf("alloc after reopen: %v", err)
	}
	if start != 5 {
		t.Fatalf("alloc start after reopen = %d, want 5", start)
	}
	tx.Rollback()
}

// TestCrashRecovery 在三个注入点模拟崩溃，验证：
//   - 崩溃点前未提交的事务整体丢弃（等价回滚）
//   - 已过提交点的事务恢复后生效
//   - 无论哪种情况，总页数守恒、无重复分配
func TestCrashRecovery(t *testing.T) {
	cases := []struct {
		name      string
		hook      func(*Hooks)
		wantAlloc bool // 崩溃前的事务恢复后是否应生效
	}{
		{"ops_synced_no_commit_record", func(h *Hooks) {
			h.AfterJournalOpsSync = func() error { return errInjected }
		}, false},
		{"commit_record_synced", func(h *Hooks) {
			h.AfterJournalCommitSync = func() error { return errInjected }
		}, true},
		{"state_persisted_journal_left", func(h *Hooks) {
			h.AfterStatePersist = func() error { return errInjected }
		}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			const total = 32

			// 先提交 5 页作为基线
			a := openAt(t, dir, total, nil)
			tx := a.Begin()
			base, _ := tx.Alloc(5)
			if err := tx.Commit(); err != nil {
				t.Fatalf("baseline commit: %v", err)
			}

			// 注入故障后提交第二个事务，模拟崩溃
			hooks := &Hooks{}
			tc.hook(hooks)
			crashed := openAt(t, dir, 0, hooks)
			tx = crashed.Begin()
			victim, _ := tx.Alloc(7)
			err := tx.Commit()
			if !errors.Is(err, errInjected) {
				t.Fatalf("commit err = %v, want injected crash", err)
			}

			// 模拟进程重启：丢弃内存态，重新 Open 走恢复
			recovered := openAt(t, dir, 0, nil)

			allocated := map[uint64]bool{}
			for i := uint64(0); i < 5; i++ {
				allocated[base+i] = true
			}
			if tc.wantAlloc {
				// 恢复后事务已生效：这些页必须处于占用态
				tx = recovered.Begin()
				if err := tx.Free(victim); err != nil {
					t.Fatalf("victim pages not allocated after recovery: %v", err)
				}
				tx.Rollback()
				for i := uint64(0); i < 7; i++ {
					allocated[victim+i] = true
				}
			} else {
				// 恢复后事务已回滚：这些页必须可重新申请
				tx = recovered.Begin()
				got, err := tx.Alloc(7)
				if err != nil {
					t.Fatalf("rolled-back pages not reusable: %v", err)
				}
				if got != victim {
					t.Fatalf("re-alloc start = %d, want %d", got, victim)
				}
				tx.Rollback()
			}

			// 总页数守恒：抽干剩余页，不得重复、不得缺页
			drain(t, recovered, allocated)
			if len(allocated) != total {
				t.Fatalf("page conservation violated: %d distinct pages, want %d",
					len(allocated), total)
			}
			if got := recovered.FreeCount(); got != 0 {
				t.Fatalf("free count after drain = %d, want 0", got)
			}
		})
	}
}

// TestTornJournal 撕裂/损坏的日志按未提交处理，不影响状态。
func TestTornJournal(t *testing.T) {
	dir := t.TempDir()
	a := openAt(t, dir, 8, nil)
	tx := a.Begin()
	if _, err := tx.Alloc(2); err != nil {
		t.Fatalf("alloc: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}

	good, err := os.ReadFile(filepath.Join(dir, stateFile))
	if err != nil {
		t.Fatalf("read state: %v", err)
	}

	torn := [][]byte{
		[]byte("PGJ"), // 半截头部
		encodeJournalOps([]op{{typ: opAlloc, start: 2, count: 3}}),                                             // 无 commit 记录
		append(encodeJournalOps([]op{{typ: opAlloc, start: 2, count: 3}}), commitMark, 0xde, 0xad, 0xbe, 0xef), // CRC 错误
	}
	for i, j := range torn {
		if err := os.WriteFile(filepath.Join(dir, journalFile), j, 0o644); err != nil {
			t.Fatalf("write torn journal: %v", err)
		}
		a2 := openAt(t, dir, 0, nil)
		if got := a2.FreeCount(); got != 6 {
			t.Fatalf("case %d: free count = %d, want 6 (torn journal ignored)", i, got)
		}
		after, _ := os.ReadFile(filepath.Join(dir, stateFile))
		if string(after) != string(good) {
			t.Fatalf("case %d: state file modified by torn journal", i)
		}
	}
}

// TestPageCountMismatch 重新打开时页数不一致必须报错，防止误挂数据目录。
func TestPageCountMismatch(t *testing.T) {
	dir := t.TempDir()
	openAt(t, dir, 8, nil)
	if _, err := Open(Options{Dir: dir, PageCount: 16}); err == nil {
		t.Fatal("Open with mismatched PageCount succeeded")
	}
}

// TestConcurrentTxns 并发事务被串行化，已占用页不会重复分配。
func TestConcurrentTxns(t *testing.T) {
	a := openAt(t, t.TempDir(), 64, nil)
	allocated := make(chan uint64, 64)
	done := make(chan struct{})
	for w := 0; w < 4; w++ {
		go func() {
			for {
				select {
				case <-done:
					return
				default:
				}
				tx := a.Begin()
				start, err := tx.Alloc(1)
				if errors.Is(err, ErrNoSpace) {
					tx.Rollback()
					return
				}
				if err == nil {
					if err := tx.Commit(); err == nil {
						allocated <- start
					}
				}
			}
		}()
	}
	seen := map[uint64]bool{}
	for i := 0; i < 64; i++ {
		seen[<-allocated] = true
	}
	close(done)
	if len(seen) != 64 {
		t.Fatalf("duplicate allocation under concurrency: %d distinct of 64", len(seen))
	}
}

func ExampleAllocator() {
	dir, _ := os.MkdirTemp("", "pagealloc-example")
	defer os.RemoveAll(dir)

	a, _ := Open(Options{Dir: dir, PageCount: 8})
	tx := a.Begin()
	start, _ := tx.Alloc(3)
	fmt.Printf("allocated pages [%d,%d)\n", start, start+3)
	tx.Rollback()
	fmt.Println("rolled back, free:", a.FreeCount())

	tx = a.Begin()
	start, _ = tx.Alloc(3)
	tx.Commit()
	fmt.Printf("re-allocated pages [%d,%d), free: %d\n", start, start+3, a.FreeCount())
	// Output:
	// allocated pages [0,3)
	// rolled back, free: 8
	// re-allocated pages [0,3), free: 5
}
