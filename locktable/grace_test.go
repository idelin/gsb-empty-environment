package locktable_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"example.org/netsec/q0463/locktable"
)

// 断开后锁进入 10 秒宽限期：持断开前令牌的新连接可原样重取，
// 区间与锁类型保持不变，且令牌一次性消费。
func TestGracePeriodReclaim(t *testing.T) {
	table, _ := newTestTable(t)

	oldConn, _ := table.Connect(false)
	token, err := table.IssueReclaimToken(oldConn)
	if err != nil {
		t.Fatalf("issue reclaim token: %v", err)
	}
	if token == "" {
		t.Fatal("reclaim token is empty")
	}

	if err := table.TryLock(oldConn, testOwner, byteRange(10, 20), locktable.WriteLock); err != nil {
		t.Fatalf("write lock: %v", err)
	}
	if err := table.TryLock(oldConn, testOwner, byteRange(30, 40), locktable.ReadLock); err != nil {
		t.Fatalf("read lock: %v", err)
	}

	table.Disconnect(oldConn)
	if table.GraceCount() != 1 {
		t.Fatalf("grace count = %d, want 1", table.GraceCount())
	}

	// 宽限期内他人仍不能接管这些区间。
	intruder, _ := table.Connect(false)
	err = table.TryLock(intruder, locktable.LkOwner("intruder"), byteRange(10, 20), locktable.WriteLock)
	if !errors.Is(err, locktable.ErrConflict) {
		t.Fatalf("grace-period takeover = %v, want ErrConflict", err)
	}

	// 正确令牌 + 新连接：原样取回全部锁区间与类型。
	newConn, _ := table.Connect(false)
	if err := table.Reclaim(newConn, testOwner, token); err != nil {
		t.Fatalf("reclaim with valid token: %v", err)
	}
	if table.GraceCount() != 0 {
		t.Fatalf("grace record not consumed: %d", table.GraceCount())
	}
	got := table.HeldLocks(newConn, testOwner)
	want := []locktable.HeldLock{
		{Range: byteRange(10, 20), Type: locktable.WriteLock},
		{Range: byteRange(30, 40), Type: locktable.ReadLock},
	}
	if len(got) != len(want) {
		t.Fatalf("reclaimed locks = %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("reclaimed locks = %+v, want %+v", got, want)
		}
	}

	// 令牌已消费，再次重取必须失败。
	again, _ := table.Connect(false)
	err = table.Reclaim(again, testOwner, token)
	if !errors.Is(err, locktable.ErrGraceExpired) {
		t.Fatalf("token reuse = %v, want ErrGraceExpired", err)
	}

}

// 超过 10 秒宽限期后锁被清除，旧令牌失效，区间可被新属主取得。
func TestGraceExpiryClearsLocksAndToken(t *testing.T) {
	table, clock := newTestTable(t)

	oldConn, _ := table.Connect(false)
	token, _ := table.IssueReclaimToken(oldConn)
	if err := table.TryLock(oldConn, testOwner, byteRange(0, 9), locktable.WriteLock); err != nil {
		t.Fatalf("write lock: %v", err)
	}
	table.Disconnect(oldConn)

	clock.WaitForTimers(1)
	clock.Advance(locktable.GracePeriod)
	waitFor(t, func() bool { return table.GraceCount() == 0 })

	newConn, _ := table.Connect(false)
	err := table.Reclaim(newConn, testOwner, token)
	if !errors.Is(err, locktable.ErrGraceExpired) {
		t.Fatalf("expired reclaim = %v, want ErrGraceExpired", err)
	}

	err = table.TryLock(newConn, locktable.LkOwner("new-tenant"), byteRange(0, 9), locktable.WriteLock)
	if err != nil {
		t.Fatalf("lock after grace expiry: %v", err)
	}
}

// 令牌错配必须拒绝；断开前未申请令牌的连接，其锁不可重取。
func TestReclaimTokenMismatchAndMissingToken(t *testing.T) {
	table, clock := newTestTable(t)

	withToken, _ := table.Connect(false)
	token, _ := table.IssueReclaimToken(withToken)
	if err := table.TryLock(withToken, locktable.LkOwner("a"), byteRange(0, 9), locktable.WriteLock); err != nil {
		t.Fatalf("lock a: %v", err)
	}

	noToken, _ := table.Connect(false)
	if err := table.TryLock(noToken, locktable.LkOwner("b"), byteRange(100, 109), locktable.WriteLock); err != nil {
		t.Fatalf("lock b: %v", err)
	}

	table.Disconnect(withToken)
	table.Disconnect(noToken)

	newConn, _ := table.Connect(false)

	// 伪造/错误令牌：恒定时间比较失败，锁保持在宽限期内不被转移。
	err := table.Reclaim(newConn, locktable.LkOwner("a"), locktable.ReclaimToken("forged-token"))
	if !errors.Is(err, locktable.ErrTokenMismatch) {
		t.Fatalf("mismatched token reclaim = %v, want ErrTokenMismatch", err)
	}
	if table.GraceCount() != 2 {
		t.Fatalf("grace count after failed reclaim = %d, want 2", table.GraceCount())
	}

	// 断开前未发放令牌：明确区分于令牌错配。
	err = table.Reclaim(newConn, locktable.LkOwner("b"), token)
	if !errors.Is(err, locktable.ErrNoReclaimToken) {
		t.Fatalf("tokenless reclaim = %v, want ErrNoReclaimToken", err)
	}

	// 未到 10 秒，正确令牌依然可以重取，证明失败尝试没有破坏状态。
	clock.Advance(locktable.GracePeriod - time.Second)
	if err := table.Reclaim(newConn, locktable.LkOwner("a"), token); err != nil {
		t.Fatalf("valid reclaim after failed attempts: %v", err)
	}
}

// 攻击者在新连接上同时伪造 lk-owner 与 reclaim 令牌：令牌比较失败，
// 受害者的锁原样保留在宽限期，且随后仍可被合法新连接取回。
func TestReclaimWithForgedTokenDoesNotTransferLocks(t *testing.T) {
	table, _ := newTestTable(t)

	victim, _ := table.Connect(false)
	token, _ := table.IssueReclaimToken(victim)
	if err := table.TryLock(victim, testOwner, byteRange(0, 127), locktable.WriteLock); err != nil {
		t.Fatalf("victim lock: %v", err)
	}
	table.Disconnect(victim)

	attacker, _ := table.Connect(false)
	err := table.Reclaim(attacker, testOwner, locktable.ReclaimToken("attacker-guessed-token"))
	if !errors.Is(err, locktable.ErrTokenMismatch) {
		t.Fatalf("forged token reclaim = %v, want ErrTokenMismatch", err)
	}
	if got := table.HeldLocks(attacker, testOwner); len(got) != 0 {
		t.Fatalf("attacker acquired locks via forged token: %+v", got)
	}
	if table.GraceCount() != 1 {
		t.Fatalf("grace record must survive failed reclaim, count = %d", table.GraceCount())
	}

	// 真令牌持有者在另一条新连接上仍然可以正常重取。
	legit, _ := table.Connect(false)
	if err := table.Reclaim(legit, testOwner, token); err != nil {
		t.Fatalf("legitimate reclaim after attack: %v", err)
	}
	got := table.HeldLocks(legit, testOwner)
	if len(got) != 1 || got[0].Range != byteRange(0, 127) || got[0].Type != locktable.WriteLock {
		t.Fatalf("reclaimed locks altered: %+v", got)
	}
}

// 阻塞队列中的请求在属主连接断开后立即移除并失败，不会在断连后获授锁。
func TestBlockerRemovedImmediatelyOnDisconnect(t *testing.T) {
	table, _ := newTestTable(t)

	holder, _ := table.Connect(false)
	if err := table.TryLock(holder, locktable.LkOwner("holder"), byteRange(0, 99), locktable.WriteLock); err != nil {
		t.Fatalf("holder lock: %v", err)
	}

	waiterConn, _ := table.Connect(false)
	result := make(chan error, 1)
	go func() {
		result <- table.Lock(context.Background(), waiterConn, locktable.LkOwner("waiter"),
			byteRange(0, 99), locktable.WriteLock)
	}()
	waitFor(t, func() bool { return table.Pending() == 1 })

	table.Disconnect(waiterConn)

	select {
	case err := <-result:
		if !errors.Is(err, locktable.ErrBlockerGone) {
			t.Fatalf("blocker result = %v, want ErrBlockerGone", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("blocker was not unblocked after disconnect")
	}
	if table.Pending() != 0 {
		t.Fatalf("pending = %d, want 0", table.Pending())
	}

	// 持有者释放锁后，队列里没有遗留请求会突然获授。
	if err := table.Unlock(holder, locktable.LkOwner("holder"), byteRange(0, 99)); err != nil {
		t.Fatalf("holder unlock: %v", err)
	}
}

// clear-locks 管理操作要求管理身份；普通连接被拒绝，管理连接可清空或按区间清除。
func TestClearLocksRequiresAdminIdentity(t *testing.T) {
	table, _ := newTestTable(t)

	plain, _ := table.Connect(false)
	admin, _ := table.Connect(true)

	if err := table.TryLock(plain, locktable.LkOwner("u"), byteRange(0, 50), locktable.WriteLock); err != nil {
		t.Fatalf("user lock: %v", err)
	}

	err := table.ClearLocks(plain, nil)
	if !errors.Is(err, locktable.ErrPermissionDenied) {
		t.Fatalf("non-admin clear-locks = %v, want ErrPermissionDenied", err)
	}
	if len(table.HeldLocks(plain, locktable.LkOwner("u"))) != 1 {
		t.Fatal("non-admin clear-locks must not remove any locks")
	}

	// 管理连接可按区间清除，剩余区间被拆分保留。
	scoped := byteRange(10, 20)
	if err := table.ClearLocks(admin, &scoped); err != nil {
		t.Fatalf("admin scoped clear-locks: %v", err)
	}
	got := table.HeldLocks(plain, locktable.LkOwner("u"))
	if len(got) != 2 || got[0].Range != byteRange(0, 9) || got[1].Range != byteRange(21, 50) {
		t.Fatalf("locks after scoped clear = %+v", got)
	}

	// 全量 clear-locks 清空所有锁。
	if err := table.ClearLocks(admin, nil); err != nil {
		t.Fatalf("admin full clear-locks: %v", err)
	}
	if len(table.HeldLocks(plain, locktable.LkOwner("u"))) != 0 {
		t.Fatal("full clear-locks must remove all locks")
	}
}
