package aries

import (
	"encoding/binary"
	"fmt"
	"runtime"
	"sync"
	"testing"
	"time"
)

// TestConcurrentWorkloadWithSnapshots 并发压力测试(配合 -race):
// 多 worker 并发执行事务(提交/中止混合),同时后台持续做
// 增量快照与检查点;最后崩溃恢复,校验磁盘与日志严格一致。
func TestConcurrentWorkloadWithSnapshots(t *testing.T) {
	dir := t.TempDir()
	db, err := OpenDB(dir, 128)
	if err != nil {
		t.Fatal(err)
	}
	sn, err := NewSnapshotter(db.BufferPool(), db.Log(), dir+"/snaps")
	if err != nil {
		t.Fatal(err)
	}

	const workers = 8
	const txnsPerWorker = 60
	stop := make(chan struct{})
	var wg sync.WaitGroup

	// 后台:增量快照。
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
				if _, err := sn.TakeIncremental(); err != nil {
					t.Errorf("snapshot: %v", err)
					return
				}
				time.Sleep(time.Millisecond)
			}
		}
	}()
	// 后台:检查点。
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
				if _, err := db.Checkpoint(); err != nil {
					t.Errorf("checkpoint: %v", err)
					return
				}
				time.Sleep(2 * time.Millisecond)
			}
		}
	}()
	// 前台:并发事务。
	var workersWG sync.WaitGroup
	for w := 0; w < workers; w++ {
		workersWG.Add(1)
		go func(worker int) {
			defer workersWG.Done()
			for i := 0; i < txnsPerWorker; i++ {
				txn, err := db.Begin()
				if err != nil {
					t.Errorf("begin: %v", err)
					return
				}
				page := uint64((worker*7 + i) % 16)
				off := ((worker + i) % 8) * 8
				var val [8]byte
				binary.LittleEndian.PutUint64(val[:], uint64(worker<<32|i))
				if err := db.Update(txn, page, off, val[:]); err != nil {
					t.Errorf("update: %v", err)
					return
				}
				if i%10 == 9 { // 10% 中止
					if err := db.Abort(txn); err != nil {
						t.Errorf("abort: %v", err)
						return
					}
				} else if err := db.Commit(txn); err != nil {
					t.Errorf("commit: %v", err)
					return
				}
			}
		}(w)
	}
	workersWG.Wait()
	close(stop)
	wg.Wait()

	// 崩溃:不刷缓冲池。
	if err := db.Log().SyncAll(); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	// 恢复并校验:磁盘每页 pageLSN == 日志中该页最大修改 LSN。
	rdb, _, err := RecoverDB(dir, 128)
	if err != nil {
		t.Fatal(err)
	}
	defer rdb.Close()
	assertDiskConsistentWithLog(t, rdb.Log(), rdb.Disk())
}

// TestNoGoroutineLeak 断言完整生命周期后无 goroutine 泄漏。
func TestNoGoroutineLeak(t *testing.T) {
	before := runtime.NumGoroutine()
	for round := 0; round < 3; round++ {
		func() {
			dir := t.TempDir()
			db, err := OpenDB(dir, 32)
			if err != nil {
				t.Fatal(err)
			}
			sn, err := NewSnapshotter(db.BufferPool(), db.Log(), dir+"/snaps")
			if err != nil {
				t.Fatal(err)
			}
			var wg sync.WaitGroup
			for w := 0; w < 4; w++ {
				wg.Add(1)
				go func(w int) {
					defer wg.Done()
					for i := 0; i < 20; i++ {
						txn, err := db.Begin()
						if err != nil {
							return
						}
						_ = db.Update(txn, uint64(w), i*8, []byte(fmt.Sprintf("w%02d-i%02d", w, i)))
						if i%3 == 0 {
							_ = db.Abort(txn)
						} else {
							_ = db.Commit(txn)
						}
					}
				}(w)
			}
			wg.Wait()
			if _, err := sn.TakeIncremental(); err != nil {
				t.Fatal(err)
			}
			if _, err := db.Checkpoint(); err != nil {
				t.Fatal(err)
			}
			if err := db.Shutdown(); err != nil {
				t.Fatal(err)
			}
			// 恢复后干净关闭。
			rdb, _, err := RecoverDB(dir, 32)
			if err != nil {
				t.Fatal(err)
			}
			if err := rdb.Shutdown(); err != nil {
				t.Fatal(err)
			}
		}()
	}
	// 给运行时一点时间回收,然后严格断言。
	var after int
	for i := 0; i < 100; i++ {
		runtime.GC()
		time.Sleep(5 * time.Millisecond)
		after = runtime.NumGoroutine()
		if after <= before {
			break
		}
	}
	if after > before {
		t.Fatalf("goroutine leak: before=%d after=%d", before, after)
	}
}

// TestClosedDBErrors 断言关闭后所有操作返回错误而非 panic(无句柄泄漏路径)。
func TestClosedDBErrors(t *testing.T) {
	dir := t.TempDir()
	db, _ := OpenDB(dir, 16)
	txn, _ := db.Begin()
	_ = db.Update(txn, 1, 0, []byte("x"))
	if err := db.Shutdown(); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Begin(); err != ErrClosed {
		t.Fatalf("Begin after close: %v", err)
	}
	if err := db.Update(txn, 1, 0, []byte("y")); err != ErrClosed {
		t.Fatalf("Update after close: %v", err)
	}
	if _, err := db.Checkpoint(); err != ErrClosed {
		t.Fatalf("Checkpoint after close: %v", err)
	}
	if _, err := db.Log().Append(&LogRecord{Type: TypeBegin}); err != ErrClosed {
		t.Fatalf("Append after close: %v", err)
	}
	if err := db.Log().Flush(1); err != ErrClosed {
		t.Fatalf("Flush after close: %v", err)
	}
	if _, _, err := db.Disk().ReadPage(1); err != ErrClosed {
		t.Fatalf("ReadPage after close: %v", err)
	}
	// 重复关闭是安全的。
	if err := db.Close(); err != nil {
		t.Fatalf("double close: %v", err)
	}
}
