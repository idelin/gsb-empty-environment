package locktable_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"example.org/netsec/q0463/locktable"
)

// 阻塞请求在锁释放后按 FIFO 获授；ctx 取消会把请求移出队列。
func TestBlockingLockGrantAndContextCancel(t *testing.T) {
	table, _ := newTestTable(t)

	holder, _ := table.Connect(false)
	if err := table.TryLock(holder, locktable.LkOwner("holder"), byteRange(0, 99), locktable.WriteLock); err != nil {
		t.Fatalf("holder lock: %v", err)
	}

	firstConn, _ := table.Connect(false)
	firstErr := make(chan error, 1)
	go func() {
		firstErr <- table.Lock(context.Background(), firstConn, locktable.LkOwner("first"),
			byteRange(0, 99), locktable.WriteLock)
	}()
	waitFor(t, func() bool { return table.Pending() == 1 })

	secondConn, _ := table.Connect(false)
	secondErr := make(chan error, 1)
	go func() {
		secondErr <- table.Lock(context.Background(), secondConn, locktable.LkOwner("second"),
			byteRange(0, 99), locktable.WriteLock)
	}()
	waitFor(t, func() bool { return table.Pending() == 2 })

	// 排在后面的请求先取消，队首请求不受影响。
	ctx, cancel := context.WithCancel(context.Background())
	cancelErr := make(chan error, 1)
	go func() {
		cancelErr <- table.Lock(ctx, secondConn, locktable.LkOwner("cancel-me"),
			byteRange(50, 60), locktable.WriteLock)
	}()
	waitFor(t, func() bool { return table.Pending() == 3 })
	cancel()
	select {
	case err := <-cancelErr:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancel result = %v, want context.Canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("canceled request never returned")
	}
	waitFor(t, func() bool { return table.Pending() == 2 })

	// 持有者释放后，队首请求获授并出队；第二个请求继续等待。
	if err := table.Unlock(holder, locktable.LkOwner("holder"), byteRange(0, 99)); err != nil {
		t.Fatalf("holder unlock: %v", err)
	}
	select {
	case err := <-firstErr:
		if err != nil {
			t.Fatalf("first waiter grant: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("first waiter was not granted")
	}
	waitFor(t, func() bool { return table.Pending() == 1 })

	if len(table.HeldLocks(firstConn, locktable.LkOwner("first"))) != 1 {
		t.Fatal("first waiter does not hold the granted lock")
	}

	// 管理操作清空全部锁后，剩余阻塞请求以 ErrLocksCleared 失败。
	admin, _ := table.Connect(true)
	if err := table.ClearLocks(admin, nil); err != nil {
		t.Fatalf("admin clear-locks: %v", err)
	}
	select {
	case err := <-secondErr:
		if !errors.Is(err, locktable.ErrLocksCleared) {
			t.Fatalf("second waiter result = %v, want ErrLocksCleared", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("second waiter was not removed by clear-locks")
	}
	waitFor(t, func() bool { return table.Pending() == 0 })
}

// 不同属主的读锁可共享；同一区间的写锁与读锁互斥。
func TestReadLocksSharedAndWriteExclusive(t *testing.T) {
	table, _ := newTestTable(t)
	connA, _ := table.Connect(false)
	connB, _ := table.Connect(false)

	if err := table.TryLock(connA, locktable.LkOwner("a"), byteRange(0, 10), locktable.ReadLock); err != nil {
		t.Fatalf("read lock a: %v", err)
	}
	if err := table.TryLock(connB, locktable.LkOwner("b"), byteRange(0, 10), locktable.ReadLock); err != nil {
		t.Fatalf("shared read lock b: %v", err)
	}
	err := table.TryLock(connB, locktable.LkOwner("b"), byteRange(5, 6), locktable.WriteLock)
	if !errors.Is(err, locktable.ErrConflict) {
		t.Fatalf("write against read locks = %v, want ErrConflict", err)
	}

	// 非法区间被拒绝。
	if err := table.TryLock(connA, locktable.LkOwner("a"), locktable.ByteRange{Start: 9, End: 1}, locktable.ReadLock); !errors.Is(err, locktable.ErrInvalidRange) {
		t.Fatalf("invalid range lock = %v, want ErrInvalidRange", err)
	}
}
