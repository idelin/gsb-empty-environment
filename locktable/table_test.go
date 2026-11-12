package locktable_test

import (
	"errors"
	"testing"
	"time"

	"example.org/netsec/q0463/locktable"
)

const testOwner = locktable.LkOwner("gluster-owner-42")

func newTestTable(t *testing.T) (*locktable.Table, *locktable.FakeClock) {
	t.Helper()
	clock := locktable.NewFakeClock(time.Date(2026, 11, 13, 0, 0, 0, 0, time.UTC))
	table := locktable.NewForTest(clock)
	t.Cleanup(table.Close)
	return table, clock
}

func byteRange(start, end uint64) locktable.ByteRange {
	return locktable.ByteRange{Start: start, End: end}
}

func waitFor(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("condition not met before deadline")
}

// 攻击者在自己的连接上伪造受害者的 lk-owner：锁表以 (连接, lk-owner)
// 为属主键，伪造者既看不到受害者的锁，也无法在同一区间加锁。
func TestSpoofedLkOwnerCannotAccessVictimLocks(t *testing.T) {
	table, _ := newTestTable(t)

	victim, err := table.Connect(false)
	if err != nil {
		t.Fatalf("victim connect: %v", err)
	}
	attacker, err := table.Connect(false)
	if err != nil {
		t.Fatalf("attacker connect: %v", err)
	}

	if err := table.TryLock(victim, testOwner, byteRange(0, 99), locktable.WriteLock); err != nil {
		t.Fatalf("victim write lock: %v", err)
	}

	// 相同 lk-owner、不同连接属于不同属主，写锁必须冲突。
	err = table.TryLock(attacker, testOwner, byteRange(10, 20), locktable.WriteLock)
	if !errors.Is(err, locktable.ErrConflict) {
		t.Fatalf("spoofed owner write lock = %v, want ErrConflict", err)
	}
	// 读锁同样不能插入受害者的写锁区间。
	err = table.TryLock(attacker, testOwner, byteRange(10, 20), locktable.ReadLock)
	if !errors.Is(err, locktable.ErrConflict) {
		t.Fatalf("spoofed owner read lock = %v, want ErrConflict", err)
	}

	held := table.HeldLocks(victim, testOwner)
	if len(held) != 1 || held[0].Range != byteRange(0, 99) {
		t.Fatalf("victim locks altered by spoof attempt: %+v", held)
	}
	if got := table.HeldLocks(attacker, testOwner); len(got) != 0 {
		t.Fatalf("attacker should hold no locks, got %+v", got)
	}
}

// 跨连接 UNLOCK：攻击者持相同 lk-owner 也只能解析到自己的属主键，
// 受害者的锁不受影响。
func TestCrossConnectionUnlockRejected(t *testing.T) {
	table, _ := newTestTable(t)

	victim, _ := table.Connect(false)
	attacker, _ := table.Connect(false)

	if err := table.TryLock(victim, testOwner, byteRange(0, 99), locktable.WriteLock); err != nil {
		t.Fatalf("victim write lock: %v", err)
	}

	err := table.Unlock(attacker, testOwner, byteRange(0, 99))
	if !errors.Is(err, locktable.ErrLockNotHeld) {
		t.Fatalf("cross-connection unlock = %v, want ErrLockNotHeld", err)
	}

	held := table.HeldLocks(victim, testOwner)
	if len(held) != 1 || held[0].Range != byteRange(0, 99) {
		t.Fatalf("victim lock removed by cross-connection unlock: %+v", held)
	}

	// 受害者自己解锁仍然成功，证明拒绝逻辑针对的是连接而非 lk-owner。
	if err := table.Unlock(victim, testOwner, byteRange(0, 99)); err != nil {
		t.Fatalf("victim self unlock: %v", err)
	}
}

// 子区间 UNLOCK 会把持有区间拆分，且只作用于本连接。
func TestUnlockSplitsHeldRange(t *testing.T) {
	table, _ := newTestTable(t)
	conn, _ := table.Connect(false)

	if err := table.TryLock(conn, testOwner, byteRange(0, 100), locktable.WriteLock); err != nil {
		t.Fatalf("write lock: %v", err)
	}
	if err := table.Unlock(conn, testOwner, byteRange(40, 60)); err != nil {
		t.Fatalf("subrange unlock: %v", err)
	}

	got := table.HeldLocks(conn, testOwner)
	want := []locktable.HeldLock{
		{Range: byteRange(0, 39), Type: locktable.WriteLock},
		{Range: byteRange(61, 100), Type: locktable.WriteLock},
	}
	if len(got) != len(want) {
		t.Fatalf("held locks = %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("held locks = %+v, want %+v", got, want)
		}
	}

	// 被挖空的区间现在可以被其他属主加锁。
	other, _ := table.Connect(false)
	err := table.TryLock(other, locktable.LkOwner("other"), byteRange(40, 60), locktable.WriteLock)
	if err != nil {
		t.Fatalf("lock inside unlocked hole: %v", err)
	}
	// 剩余区间仍受保护。
	err = table.TryLock(other, locktable.LkOwner("other"), byteRange(0, 100), locktable.WriteLock)
	if !errors.Is(err, locktable.ErrConflict) {
		t.Fatalf("lock over remaining pieces = %v, want ErrConflict", err)
	}
}
