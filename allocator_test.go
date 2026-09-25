package pagealloc

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func openFresh(t *testing.T, total int) (*Allocator, string) {
	t.Helper()
	dir := t.TempDir()
	a, err := Open(dir, total)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	return a, dir
}

func commit(t *testing.T, tx *Tx) {
	t.Helper()
	if err := tx.Commit(); err != nil {
		t.Fatalf("Commit: %v", err)
	}
}

// 申请 3 页再回滚后，这 3 页必须可重用。
func TestRollbackMakesPagesReusable(t *testing.T) {
	a, _ := openFresh(t, 8)

	tx, _ := a.Begin()
	start, err := tx.Alloc(3)
	if err != nil {
		t.Fatalf("Alloc: %v", err)
	}
	if start != 0 {
		t.Fatalf("first-fit should return 0, got %d", start)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatalf("Rollback: %v", err)
	}

	if got := a.FreeCount(); got != 8 {
		t.Fatalf("FreeCount after rollback = %d, want 8", got)
	}
	// 回滚后同样的 3 页可以再次申请并提交。
	tx2, _ := a.Begin()
	start2, err := tx2.Alloc(3)
	if err != nil {
		t.Fatalf("re-Alloc after rollback: %v", err)
	}
	if start2 != start {
		t.Fatalf("re-Alloc start = %d, want %d", start2, start)
	}
	commit(t, tx2)
	if got := a.FreeCount(); got != 5 {
		t.Fatalf("FreeCount after commit = %d, want 5", got)
	}
}

// 相邻空闲区间必须合并为一个区间。
func TestAdjacentFreeRangesMerge(t *testing.T) {
	a, _ := openFresh(t, 10)

	tx, _ := a.Begin()
	for i := 0; i < 3; i++ {
		if _, err := tx.Alloc(1); err != nil {
			t.Fatalf("Alloc: %v", err)
		}
	}
	commit(t, tx) // 占用页 0,1,2

	tx2, _ := a.Begin()
	// 以乱序释放，制造空洞再填上。
	if err := tx2.Free(0, 1); err != nil {
		t.Fatalf("Free 0: %v", err)
	}
	if err := tx2.Free(2, 1); err != nil {
		t.Fatalf("Free 2: %v", err)
	}
	if err := tx2.Free(1, 1); err != nil {
		t.Fatalf("Free 1: %v", err)
	}
	commit(t, tx2)

	want := [][2]int{{0, 10}}
	if got := a.FreeRanges(); !reflect.DeepEqual(got, want) {
		t.Fatalf("FreeRanges = %v, want merged %v", got, want)
	}
}

// 已占用页不能被重复分配。
func TestOccupiedPagesNotReallocated(t *testing.T) {
	a, _ := openFresh(t, 4)
	tx, _ := a.Begin()
	seen := map[int]bool{}
	for i := 0; i < 4; i++ {
		p, err := tx.Alloc(1)
		if err != nil {
			t.Fatalf("Alloc %d: %v", i, err)
		}
		if seen[p] {
			t.Fatalf("page %d allocated twice", p)
		}
		seen[p] = true
	}
	if _, err := tx.Alloc(1); !errors.Is(err, ErrNoSpace) {
		t.Fatalf("Alloc on full allocator = %v, want ErrNoSpace", err)
	}
	commit(t, tx)

	// 新事务中同样无法分配到任何页。
	tx2, _ := a.Begin()
	if _, err := tx2.Alloc(1); !errors.Is(err, ErrNoSpace) {
		t.Fatalf("Alloc after commit = %v, want ErrNoSpace", err)
	}
	tx2.Rollback()
}

// 双重释放必须被拒绝。
func TestDoubleFreeRejected(t *testing.T) {
	a, _ := openFresh(t, 8)
	tx, _ := a.Begin()
	if _, err := tx.Alloc(2); err != nil {
		t.Fatalf("Alloc: %v", err)
	}
	commit(t, tx) // 占用 [0,2)

	tx2, _ := a.Begin()
	if err := tx2.Free(0, 2); err != nil {
		t.Fatalf("first Free: %v", err)
	}
	if err := tx2.Free(0, 2); !errors.Is(err, ErrDoubleFree) {
		t.Fatalf("second Free = %v, want ErrDoubleFree", err)
	}
	// 部分重叠（页 1 已空闲）也必须拒绝。
	if err := tx2.Free(1, 3); !errors.Is(err, ErrDoubleFree) {
		t.Fatalf("overlapping Free = %v, want ErrDoubleFree", err)
	}
	// 释放从未占用过的空闲页也是非法的。
	if err := tx2.Free(5, 1); !errors.Is(err, ErrDoubleFree) {
		t.Fatalf("Free of free page = %v, want ErrDoubleFree", err)
	}
	tx2.Rollback()
}

// 越界释放必须被拒绝。
func TestOutOfRangeFreeRejected(t *testing.T) {
	a, _ := openFresh(t, 8)
	tx, _ := a.Begin()
	if _, err := tx.Alloc(8); err != nil {
		t.Fatalf("Alloc: %v", err)
	}
	cases := [][2]int{{-1, 1}, {8, 1}, {7, 2}, {0, 9}, {3, 0}}
	for _, c := range cases {
		if err := tx.Free(c[0], c[1]); !errors.Is(err, ErrOutOfRange) {
			t.Fatalf("Free(%d,%d) = %v, want ErrOutOfRange", c[0], c[1], err)
		}
	}
	tx.Rollback()
}

// 提交后重新打开，回收/占用状态必须恢复。
func TestCommitPersistsAcrossReopen(t *testing.T) {
	a, dir := openFresh(t, 8)
	tx, _ := a.Begin()
	tx.Alloc(3)
	commit(t, tx)
	tx2, _ := a.Begin()
	tx2.Free(1, 2) // 回收页 1,2
	commit(t, tx2)

	a2, err := Open(dir, 0) // totalPages 被忽略，从状态文件恢复
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	if a2.Total() != 8 {
		t.Fatalf("Total = %d, want 8", a2.Total())
	}
	// 回收的 [1,3) 与空闲的 [3,8) 相邻，合并为 [1,8)。
	want := [][2]int{{1, 8}}
	if got := a2.FreeRanges(); !reflect.DeepEqual(got, want) {
		t.Fatalf("FreeRanges after reopen = %v, want %v", got, want)
	}
}

// 在每个注入点模拟崩溃：重新打开后状态必须一致（旧版本或新版本），
// 且总页数守恒（空闲 + 占用 == 总页数）。
func TestCrashAtFaultPointsConservesPages(t *testing.T) {
	points := []FaultPoint{
		FaultBeforeWrite,
		FaultBeforeFileSync,
		FaultBeforeRename,
		FaultBeforeDirSync,
	}
	for _, p := range points {
		t.Run(string(p), func(t *testing.T) {
			a, dir := openFresh(t, 16)

			// 先提交一个基线状态：占用 [0,4)。
			tx, _ := a.Begin()
			tx.Alloc(4)
			commit(t, tx)

			// 武装注入点后提交第二个事务：再占 4 页、释放 2 页。
			a.Injector().Arm(p)
			tx2, _ := a.Begin()
			tx2.Alloc(4)
			tx2.Free(1, 2)
			if err := tx2.Commit(); !errors.Is(err, ErrCrash) {
				t.Fatalf("Commit = %v, want ErrCrash", err)
			}

			// 模拟进程重启：丢弃内存状态，从目录恢复。
			a2, err := Open(dir, 0)
			if err != nil {
				t.Fatalf("reopen after crash at %s: %v", p, err)
			}
			if got := a2.FreeCount() + a2.AllocatedCount(); got != a2.Total() {
				t.Fatalf("page conservation violated: free+alloc = %d, total = %d", got, a2.Total())
			}
			// 恢复出的状态必须是基线（旧）或第二个事务的结果（新），不能撕裂。
			old := [][2]int{{4, 16}}
			new_ := [][2]int{{1, 3}, {8, 16}}
			got := a2.FreeRanges()
			if !reflect.DeepEqual(got, old) && !reflect.DeepEqual(got, new_) {
				t.Fatalf("recovered state %v is neither old %v nor new %v", got, old, new_)
			}
			// 恢复后分配器仍可正常工作。
			tx3, _ := a2.Begin()
			if _, err := tx3.Alloc(2); err != nil {
				t.Fatalf("Alloc after recovery: %v", err)
			}
			commit(t, tx3)
		})
	}
}

// 崩溃后总页数守恒：随机操作 + 注入崩溃，反复验证。
func TestCrashConservationFuzzish(t *testing.T) {
	dir := t.TempDir()
	a, err := Open(dir, 32)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	points := []FaultPoint{FaultBeforeWrite, FaultBeforeFileSync, FaultBeforeRename, FaultBeforeDirSync}
	for round := 0; round < 20; round++ {
		tx, _ := a.Begin()
		// 申请一些页，再释放一部分。
		var holds [][2]int
		for i := 0; i < 3; i++ {
			if p, err := tx.Alloc(2); err == nil {
				holds = append(holds, [2]int{p, 2})
			}
		}
		for _, h := range holds[:len(holds)/2] {
			if err := tx.Free(h[0], h[1]); err != nil {
				t.Fatalf("Free: %v", err)
			}
		}
		a.Injector().Arm(points[round%len(points)])
		err := tx.Commit()
		if err != nil && !errors.Is(err, ErrCrash) {
			t.Fatalf("Commit: %v", err)
		}
		// 无论是否“崩溃”，都重新打开验证守恒。
		a, err = Open(dir, 0)
		if err != nil {
			t.Fatalf("reopen round %d: %v", round, err)
		}
		if a.Total() != 32 {
			t.Fatalf("round %d: Total = %d, want 32", round, a.Total())
		}
		if got := a.FreeCount() + a.AllocatedCount(); got != 32 {
			t.Fatalf("round %d: conservation violated, got %d", round, got)
		}
		// 空闲区间必须有序、不重叠、在界内。
		prevEnd := 0
		for _, r := range a.FreeRanges() {
			if r[0] < prevEnd || r[0] >= r[1] || r[1] > 32 {
				t.Fatalf("round %d: bad free range %v", round, r)
			}
			prevEnd = r[1]
		}
	}
}

// 状态文件损坏（校验和不匹配）必须报错而不是静默接受。
func TestCorruptStateFileRejected(t *testing.T) {
	a, dir := openFresh(t, 8)
	tx, _ := a.Begin()
	tx.Alloc(1)
	commit(t, tx)

	path := filepath.Join(dir, stateName)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	data[len(data)-1] ^= 0xff
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if _, err := Open(dir, 0); err == nil {
		t.Fatal("Open with corrupted state file should fail")
	}
}
