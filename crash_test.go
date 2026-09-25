package diskdeque

import (
	"errors"
	"fmt"
	"math/rand"
	"path/filepath"
	"slices"
	"testing"
)

// 提交点（journal durable）之后各阶段崩溃：重开目录必须前滚出完整事务。
func TestCrashRecovery(t *testing.T) {
	points := map[string]FaultPoint{
		"after_journal":      FaultAfterJournal,
		"after_block_rename": FaultAfterBlockRename,
		"after_superblock":   FaultAfterSuperblock,
	}
	for name, p := range points {
		t.Run(name, func(t *testing.T) {
			defer ClearFaultHooks()
			dir := t.TempDir()
			d, err := Open(dir, testOpts())
			if err != nil {
				t.Fatal(err)
			}
			pushRange(t, d, true, "a", "b")

			tx := d.Begin()
			tx.PushLeft([]byte("L"))
			tx.PushRight([]byte("R"))
			SetFaultHook(p, func() error { return ErrInjected })
			if err := tx.Commit(); !errors.Is(err, ErrInjected) {
				t.Fatalf("commit = %v, want injected crash", err)
			}
			ClearFaultHooks()
			if _, err := d.ReadRange(0, 1); err == nil {
				t.Fatalf("crashed handle should be unusable")
			}
			d.Close()

			d2, err := Open(dir, testOpts())
			if err != nil {
				t.Fatalf("reopen: %v", err)
			}
			defer d2.Close()
			expectContents(t, d2, []string{"L", "a", "b", "R"})

			if fileExists(filepath.Join(dir, journalFile)) {
				t.Fatalf("journal not removed after recovery")
			}
			onDisk := blockFiles(t, dir)
			if len(onDisk) != len(d2.m.blocks) {
				t.Fatalf("on-disk blocks %v, meta blocks %v", onDisk, d2.m.blocks)
			}
			for _, id := range d2.m.blocks {
				if !onDisk[id] {
					t.Fatalf("meta references missing block %d", id)
				}
			}

			pushRange(t, d2, true, "c")
			expectContents(t, d2, []string{"L", "a", "b", "R", "c"})
		})
	}
}

// 提交点之前失败：事务整体丢弃，磁盘保持旧状态。
func TestCrashBeforeCommitPoint(t *testing.T) {
	defer ClearFaultHooks()
	dir := t.TempDir()
	d, err := Open(dir, testOpts())
	if err != nil {
		t.Fatal(err)
	}
	pushRange(t, d, true, "a", "b")

	tx := d.Begin()
	tx.PushLeft([]byte("L"))
	tx.PushRight([]byte("R"))
	SetFaultHook(FaultBeforeJournal, func() error { return ErrInjected })
	if err := tx.Commit(); !errors.Is(err, ErrInjected) {
		t.Fatalf("commit = %v", err)
	}
	d.Close()

	d2, err := Open(dir, testOpts())
	if err != nil {
		t.Fatal(err)
	}
	defer d2.Close()
	expectContents(t, d2, []string{"a", "b"})
}

// 小编号空间下随机操作 400 个事务：块 ID 多次环绕，
// 活跃块不被覆盖（以数据与模型一致、磁盘文件与元数据一致来检验）。
func TestWrapAroundIDs(t *testing.T) {
	defer ClearFaultHooks()
	rng := rand.New(rand.NewSource(42))
	dir := t.TempDir()
	opts := &Options{BlockCapacity: 2, MaxBlocks: 24}
	d, err := Open(dir, opts)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()

	var model []string
	counter := 0
	for step := 0; step < 200; step++ {
		snapshot := slices.Clone(model)
		tx := d.Begin()
		ops := 1 + rng.Intn(5)
		for op := 0; op < ops; op++ {
			switch rng.Intn(4) {
			case 0:
				counter++
				v := fmt.Sprintf("v%d", counter)
				tx.PushLeft([]byte(v))
				model = append([]string{v}, model...)
			case 1:
				counter++
				v := fmt.Sprintf("v%d", counter)
				tx.PushRight([]byte(v))
				model = append(model, v)
			case 2:
				if len(model) > 0 {
					if _, err := tx.PopLeft(); err != nil {
						t.Fatalf("step %d: PopLeft: %v", step, err)
					}
					model = model[1:]
				}
			case 3:
				if len(model) > 0 {
					if _, err := tx.PopRight(); err != nil {
						t.Fatalf("step %d: PopRight: %v", step, err)
					}
					model = model[:len(model)-1]
				}
			}
		}

		// 随机注入“提交点前崩溃”，验证回滚路径也安全
		crash := rng.Intn(10) == 0
		if crash {
			SetFaultHook(FaultBeforeJournal, func() error { return ErrInjected })
		}
		err := tx.Commit()
		ClearFaultHooks()
		if crash {
			if !errors.Is(err, ErrInjected) {
				t.Fatalf("step %d: commit = %v, want injected", step, err)
			}
			model = snapshot // 回滚：模型恢复到事务前
			continue
		}
		if err != nil {
			t.Fatalf("step %d: commit: %v", step, err)
		}
		if len(model) > 30 {
			// 控制规模，避免占满小编号空间
			tx2 := d.Begin()
			for len(model) > 10 {
				if _, err := tx2.PopRight(); err != nil {
					t.Fatalf("drain: %v", err)
				}
				model = model[:len(model)-1]
			}
			if err := tx2.Commit(); err != nil {
				t.Fatalf("drain commit: %v", err)
			}
		}
	}

	expectContents(t, d, model)

	// 重开 + 校验文件集合与元数据一致
	d.Close()
	d2, err := Open(dir, opts)
	if err != nil {
		t.Fatal(err)
	}
	defer d2.Close()
	expectContents(t, d2, model)
	onDisk := blockFiles(t, dir)
	if len(onDisk) != len(d2.m.blocks) {
		t.Fatalf("on-disk %v vs meta %v", onDisk, d2.m.blocks)
	}
	for _, id := range d2.m.blocks {
		if !onDisk[id] {
			t.Fatalf("meta references missing block %d", id)
		}
	}
}
