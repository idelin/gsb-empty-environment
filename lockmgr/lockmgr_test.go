package lockmgr

import (
	"fmt"
	"sync"
	"testing"
	"time"
)

const testTimeout = 2 * time.Second

// 等待条件成立，超时则失败。
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(testTimeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for: %s", what)
}

// TestSharedLocksCompatible: 多个事务可同时持有同一行的 S 锁。
func TestSharedLocksCompatible(t *testing.T) {
	lm := NewLockManager(WoundWait, testTimeout)
	t1, t2 := lm.Begin(), lm.Begin()
	if err := t1.Lock("r", Shared); err != nil {
		t.Fatal(err)
	}
	if err := t2.Lock("r", Shared); err != nil {
		t.Fatal(err)
	}
}

// TestExclusiveBlocksShared: X 锁与 S 锁互斥，S 请求应超时。
func TestExclusiveBlocksShared(t *testing.T) {
	lm := NewLockManager(WoundWait, 150*time.Millisecond)
	t1, t2 := lm.Begin(), lm.Begin()
	if err := t1.Lock("r", Exclusive); err != nil {
		t.Fatal(err)
	}
	// t2 比 t1 年轻，Wound-Wait 下只能等待，直至超时。
	if err := t2.Lock("r", Shared); err != ErrTimeout {
		t.Fatalf("want ErrTimeout, got %v", err)
	}
}

// TestWoundWaitYoungerWaits: 验收边界 1。
// T1(老)持有行 X 锁，T2(新)请求 X 锁 → T2 被挂起等待；T1 释放后 T2 获得锁。
func TestWoundWaitYoungerWaits(t *testing.T) {
	lm := NewLockManager(WoundWait, testTimeout)
	t1 := lm.Begin() // 老事务
	t2 := lm.Begin() // 新事务

	if err := t1.Lock("row", Exclusive); err != nil {
		t.Fatal(err)
	}

	granted := make(chan error, 1)
	go func() { granted <- t2.Lock("row", Exclusive) }()

	// T2 应在 200ms 内仍处于挂起状态（未获得锁）。
	select {
	case err := <-granted:
		t.Fatalf("T2 should be suspended, but Lock returned: %v", err)
	case <-time.After(200 * time.Millisecond):
	}
	if t2.Aborted() {
		t.Fatal("T2 must wait, not be aborted")
	}

	// T1 提交释放锁后，T2 应被唤醒并获得锁。
	t1.Commit()
	select {
	case err := <-granted:
		if err != nil {
			t.Fatalf("T2 should acquire lock after T1 commits, got %v", err)
		}
	case <-time.After(testTimeout):
		t.Fatal("T2 still blocked after T1 released the lock")
	}
}

// TestWoundWaitOlderWoundsYounger: 验收边界 2。
// T2(新)持有行 X 锁，T1(老)请求 X 锁 → T1 直接抢占(Wound) T2，
// T2 被中止需重启，T1 立即获得锁。
func TestWoundWaitOlderWoundsYounger(t *testing.T) {
	lm := NewLockManager(WoundWait, testTimeout)
	t1 := lm.Begin() // 老事务
	t2 := lm.Begin() // 新事务

	if err := t2.Lock("row", Exclusive); err != nil {
		t.Fatal(err)
	}

	// 老事务 T1 请求同一行：应抢占 T2 并立即获得锁。
	start := time.Now()
	if err := t1.Lock("row", Exclusive); err != nil {
		t.Fatalf("T1 should wound T2 and get the lock, got %v", err)
	}
	if elapsed := time.Since(start); elapsed > 100*time.Millisecond {
		t.Fatalf("T1 should be granted immediately after wounding, took %v", elapsed)
	}
	if !t2.Aborted() {
		t.Fatal("T2 should have been wounded (aborted)")
	}
	// 被抢占的 T2 后续任何锁请求都应返回 ErrAborted。
	if err := t2.Lock("row", Shared); err != ErrAborted {
		t.Fatalf("wounded T2 should get ErrAborted, got %v", err)
	}

	// T2 以新事务身份重启，T1 释放后可重新获得锁。
	t2Restart := lm.Begin()
	done := make(chan error, 1)
	go func() { done <- t2Restart.Lock("row", Exclusive) }()
	t1.Commit()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("restarted T2 should acquire lock, got %v", err)
		}
	case <-time.After(testTimeout):
		t.Fatal("restarted T2 still blocked")
	}
}

// TestWaitDieYoungerDies: Wait-Die 下年轻事务遇到老事务持锁时立即自杀。
func TestWaitDieYoungerDies(t *testing.T) {
	lm := NewLockManager(WaitDie, testTimeout)
	t1 := lm.Begin() // 老
	t2 := lm.Begin() // 新

	if err := t1.Lock("row", Exclusive); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	if err := t2.Lock("row", Exclusive); err != ErrAborted {
		t.Fatalf("younger txn should die, got %v", err)
	}
	if elapsed := time.Since(start); elapsed > 100*time.Millisecond {
		t.Fatalf("younger txn should die immediately, took %v", elapsed)
	}
	if !t2.Aborted() {
		t.Fatal("T2 should be marked aborted")
	}
}

// TestWaitDieOlderWaits: Wait-Die 下老事务等待年轻事务释放锁。
func TestWaitDieOlderWaits(t *testing.T) {
	lm := NewLockManager(WaitDie, testTimeout)
	t1 := lm.Begin() // 老
	t2 := lm.Begin() // 新

	if err := t2.Lock("row", Exclusive); err != nil {
		t.Fatal(err)
	}
	granted := make(chan error, 1)
	go func() { granted <- t1.Lock("row", Exclusive) }()

	select {
	case err := <-granted:
		t.Fatalf("older txn should wait for younger holder, got %v", err)
	case <-time.After(200 * time.Millisecond):
	}

	t2.Commit()
	select {
	case err := <-granted:
		if err != nil {
			t.Fatalf("older txn should get lock after younger commits, got %v", err)
		}
	case <-time.After(testTimeout):
		t.Fatal("older txn still blocked")
	}
}

// TestLockTimeout: 等待超过超时时间返回 ErrTimeout。
func TestLockTimeout(t *testing.T) {
	lm := NewLockManager(WoundWait, 150*time.Millisecond)
	t1 := lm.Begin()
	t2 := lm.Begin()
	if err := t1.Lock("row", Exclusive); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	if err := t2.Lock("row", Exclusive); err != ErrTimeout {
		t.Fatalf("want ErrTimeout, got %v", err)
	}
	if elapsed := time.Since(start); elapsed < 150*time.Millisecond {
		t.Fatalf("returned too early: %v", elapsed)
	}
}

// TestLockUpgrade: 同事务 S 锁可升级为 X 锁。
func TestLockUpgrade(t *testing.T) {
	lm := NewLockManager(WoundWait, testTimeout)
	txn := lm.Begin()
	if err := txn.Lock("row", Shared); err != nil {
		t.Fatal(err)
	}
	if err := txn.Lock("row", Exclusive); err != nil {
		t.Fatalf("self upgrade S->X should succeed, got %v", err)
	}
}

// TestNoDeadlockUnderContention: 两种策略下高并发随机抢锁，
// 所有事务（中止后自动重启）最终都能完成，验证系统全局无死锁。
func TestNoDeadlockUnderContention(t *testing.T) {
	for _, policy := range []Policy{WaitDie, WoundWait} {
		t.Run(policy.String(), func(t *testing.T) {
			lm := NewLockManager(policy, testTimeout)
			rows := []string{"a", "b", "c"}
			var wg sync.WaitGroup
			for worker := 0; worker < 16; worker++ {
				wg.Add(1)
				go func(seed int) {
					defer wg.Done()
					for i := 0; i < 20; i++ {
						txn := lm.Begin()
						err := runUnitOfWork(txn, rows, seed+i)
						if err == ErrAborted || err == ErrTimeout {
							txn.Rollback()
							i-- // 中止/超时后重启重试
							continue
						}
						if err != nil {
							t.Errorf("unexpected error: %v", err)
						}
						txn.Commit()
					}
				}(worker)
			}
			done := make(chan struct{})
			go func() { wg.Wait(); close(done) }()
			select {
			case <-done:
			case <-time.After(30 * time.Second):
				t.Fatal("possible deadlock: workers did not finish in 30s")
			}
		})
	}
}

// runUnitOfWork 以确定性的伪随机顺序对多行加 X 锁。
func runUnitOfWork(txn *Txn, rows []string, seed int) error {
	n := 2 + seed%2
	for i := 0; i < n; i++ {
		row := rows[(seed+i*7)%len(rows)]
		if err := txn.Lock(row, Exclusive); err != nil {
			return err
		}
	}
	return nil
}

// Example 演示 Wound-Wait 的抢占行为。
func Example() {
	lm := NewLockManager(WoundWait, time.Second)
	t1 := lm.Begin() // 老事务
	t2 := lm.Begin() // 新事务

	_ = t2.Lock("row", Exclusive)            // 新事务先持锁
	err := t1.Lock("row", Exclusive)         // 老事务抢占
	fmt.Println("T1 lock:", err)             // 老事务立即获得锁
	fmt.Println("T2 aborted:", t2.Aborted()) // 新事务被 wound
	// Output:
	// T1 lock: <nil>
	// T2 aborted: true
}
