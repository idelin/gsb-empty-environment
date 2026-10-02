package lockmgr

import (
	"testing"
	"time"
)

// 异步请求锁，返回结果通道
func lockAsync(m *Manager, txnID uint64, row string, mode LockMode, timeout time.Duration) chan error {
	ch := make(chan error, 1)
	go func() { ch <- m.Lock(txnID, row, mode, timeout) }()
	return ch
}

// 断言在 d 时间内没有结果（仍在等待）
func assertBlocked(t *testing.T, ch chan error, d time.Duration, msg string) {
	t.Helper()
	select {
	case err := <-ch:
		t.Fatalf("%s: 期望仍在等待, 却收到结果 %v", msg, err)
	case <-time.After(d):
	}
}

// 断言在 d 时间内收到结果
func assertResult(t *testing.T, ch chan error, d time.Duration, want error, msg string) {
	t.Helper()
	select {
	case err := <-ch:
		if err != want {
			t.Fatalf("%s: 期望 %v, 得到 %v", msg, want, err)
		}
	case <-time.After(d):
		t.Fatalf("%s: 超时未收到结果", msg)
	}
}

// 验收场景1(Wound-Wait): 老事务T1持有X锁, 新事务T2请求X锁 -> T2挂起等待
func TestWoundWait_YoungWaitsForOld(t *testing.T) {
	m := NewManager(WoundWait)
	const oldTxn, youngTxn = 1, 2

	if err := m.Lock(oldTxn, "rowA", Exclusive, 0); err != nil {
		t.Fatalf("T1加锁失败: %v", err)
	}

	done := lockAsync(m, youngTxn, "rowA", Exclusive, 2*time.Second)
	assertBlocked(t, done, 200*time.Millisecond, "T2(新)应挂起等待T1(老)")

	// 老事务提交后, 新事务获得锁
	m.Commit(oldTxn)
	assertResult(t, done, time.Second, nil, "T1释放后T2应获得锁")
	if !m.Holds(youngTxn, "rowA") {
		t.Fatal("T2应持有rowA的X锁")
	}
	if m.IsAborted(youngTxn) {
		t.Fatal("T2不应被中止")
	}
}

// 验收场景2(Wound-Wait): 新事务T2持有X锁, 老事务T1请求 -> T1直接抢占(Wound), T2被迫重启
func TestWoundWait_OldWoundsYoung(t *testing.T) {
	m := NewManager(WoundWait)
	const oldTxn, youngTxn = 1, 2

	if err := m.Lock(youngTxn, "rowA", Exclusive, 0); err != nil {
		t.Fatalf("T2加锁失败: %v", err)
	}

	// 老事务请求同一行: 应立即抢占成功
	start := time.Now()
	if err := m.Lock(oldTxn, "rowA", Exclusive, time.Second); err != nil {
		t.Fatalf("T1(老)应抢占成功, 得到 %v", err)
	}
	if elapsed := time.Since(start); elapsed > 100*time.Millisecond {
		t.Fatalf("抢占应立即完成, 耗时 %v", elapsed)
	}
	if !m.Holds(oldTxn, "rowA") {
		t.Fatal("T1应持有rowA的X锁")
	}
	if m.Holds(youngTxn, "rowA") {
		t.Fatal("T2的锁应被抢占释放")
	}
	if !m.IsAborted(youngTxn) {
		t.Fatal("T2应被标记为已中止(被迫重启)")
	}

	// 被Wound的事务后续操作返回ErrAborted, 直到重启
	if err := m.Lock(youngTxn, "rowB", Shared, 0); err != ErrAborted {
		t.Fatalf("被Wound的事务应得到ErrAborted, 得到 %v", err)
	}
	m.Restart(youngTxn)
	if err := m.Lock(youngTxn, "rowB", Shared, 0); err != nil {
		t.Fatalf("重启后应能正常加锁, 得到 %v", err)
	}
}

// Wound-Wait: 新事务在等待队列中被老事务抢占时, 等待中的请求收到ErrAborted
func TestWoundWait_WaitingYoungWounded(t *testing.T) {
	m := NewManager(WoundWait)
	const t1, t2, t3 = 1, 2, 3

	// T2(较新)持有rowA, T3(最新)排队等待
	if err := m.Lock(t2, "rowA", Exclusive, 0); err != nil {
		t.Fatal(err)
	}
	waiting := lockAsync(m, t3, "rowA", Exclusive, 2*time.Second)
	assertBlocked(t, waiting, 100*time.Millisecond, "T3应排队等待T2")

	// T1(最老)到来, 抢占持有者T2; T3仍在队列中但随后被授权前持有者已变
	if err := m.Lock(t1, "rowA", Exclusive, time.Second); err != nil {
		t.Fatalf("T1应抢占成功: %v", err)
	}
	if !m.IsAborted(t2) {
		t.Fatal("T2应被Wound")
	}
	// T1提交后T3获得锁
	m.Commit(t1)
	assertResult(t, waiting, time.Second, nil, "T1提交后T3应获得锁")
}

// Wait-Die: 老事务持有, 新事务请求 -> 新事务自杀(Die), 重启后可获锁
func TestWaitDie_YoungDiesForOld(t *testing.T) {
	m := NewManager(WaitDie)
	const oldTxn, youngTxn = 1, 2

	if err := m.Lock(oldTxn, "rowA", Exclusive, 0); err != nil {
		t.Fatal(err)
	}
	// 新事务请求老事务持有的锁: 立即自杀而非等待
	if err := m.Lock(youngTxn, "rowA", Exclusive, 2*time.Second); err != ErrAborted {
		t.Fatalf("T2(新)应Die, 得到 %v", err)
	}
	if !m.IsAborted(youngTxn) {
		t.Fatal("T2应被标记为已中止")
	}
	// 老事务提交后, 新事务以原时间戳重启即可获锁
	m.Commit(oldTxn)
	m.Restart(youngTxn)
	if err := m.Lock(youngTxn, "rowA", Exclusive, 2*time.Second); err != nil {
		t.Fatalf("重启后T2应获得锁, 得到 %v", err)
	}
}

// Wait-Die: 新事务持有, 老事务请求 -> 老事务等待(不会死), 新事务释放后老事务获锁
func TestWaitDie_OldWaitsForYoung(t *testing.T) {
	m := NewManager(WaitDie)
	const oldTxn, youngTxn = 1, 2

	if err := m.Lock(youngTxn, "rowA", Exclusive, 0); err != nil {
		t.Fatal(err)
	}
	done := lockAsync(m, oldTxn, "rowA", Exclusive, 2*time.Second)
	assertBlocked(t, done, 200*time.Millisecond, "Wait-Die下老事务应等待新事务")

	m.Commit(youngTxn)
	assertResult(t, done, time.Second, nil, "新事务释放后老事务应获得锁")
}

// Wait-Die: 新事务请求老事务持有的锁 -> 新事务自杀(Die)
func TestWaitDie_YoungDies(t *testing.T) {
	m := NewManager(WaitDie)
	const oldTxn, youngTxn = 1, 2

	// 老事务持有rowA, 新事务持有rowB, 形成潜在循环等待
	if err := m.Lock(oldTxn, "rowA", Exclusive, 0); err != nil {
		t.Fatal(err)
	}
	if err := m.Lock(youngTxn, "rowB", Exclusive, 0); err != nil {
		t.Fatal(err)
	}
	// 老事务等rowB(等待, 因为是老的)
	oldWaiting := lockAsync(m, oldTxn, "rowB", Exclusive, 2*time.Second)
	assertBlocked(t, oldWaiting, 100*time.Millisecond, "老事务应等待新事务")

	// 新事务请求rowA -> 自杀
	if err := m.Lock(youngTxn, "rowA", Exclusive, time.Second); err != ErrAborted {
		t.Fatalf("新事务应Die, 得到 %v", err)
	}
	if !m.IsAborted(youngTxn) {
		t.Fatal("新事务应被标记中止")
	}
	// 新事务自杀释放rowB后, 老事务获得rowB -> 系统无死锁
	assertResult(t, oldWaiting, time.Second, nil, "新事务Die后老事务应获得锁")
}

// 超时: 等待超过timeout返回ErrTimeout
func TestLockTimeout(t *testing.T) {
	m := NewManager(WoundWait)
	if err := m.Lock(1, "rowA", Exclusive, 0); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	// 注意: Wound-Wait下老事务会抢占, 这里用新事务(2>1)测试超时
	if err := m.Lock(2, "rowA", Exclusive, 150*time.Millisecond); err != ErrTimeout {
		t.Fatalf("期望ErrTimeout, 得到 %v", err)
	}
	if elapsed := time.Since(start); elapsed < 150*time.Millisecond {
		t.Fatalf("应等待至超时, 实际 %v", elapsed)
	}
	// 超时后请求应已移出队列: 持有者释放后锁可被他人获取
	m.Commit(1)
	if err := m.Lock(3, "rowA", Exclusive, 100*time.Millisecond); err != nil {
		t.Fatalf("超时请求应已出队, T3应获得锁: %v", err)
	}
}

// 共享锁兼容: 多个事务可同时持有S锁
func TestSharedLocksCompatible(t *testing.T) {
	m := NewManager(WoundWait)
	if err := m.Lock(1, "rowA", Shared, 0); err != nil {
		t.Fatal(err)
	}
	if err := m.Lock(2, "rowA", Shared, 0); err != nil {
		t.Fatalf("S锁应兼容: %v", err)
	}
	// X锁与S锁冲突: 新事务(3)等待
	done := lockAsync(m, 3, "rowA", Exclusive, 2*time.Second)
	assertBlocked(t, done, 100*time.Millisecond, "X应与S冲突")
	m.Commit(1)
	assertBlocked(t, done, 100*time.Millisecond, "还有一个S持有者")
	m.Commit(2)
	assertResult(t, done, time.Second, nil, "全部S释放后X应获得锁")
}

// 锁升级: S -> X
func TestLockUpgrade(t *testing.T) {
	m := NewManager(WoundWait)
	if err := m.Lock(1, "rowA", Shared, 0); err != nil {
		t.Fatal(err)
	}
	// 唯一持有者可立即升级
	if err := m.Lock(1, "rowA", Exclusive, 0); err != nil {
		t.Fatalf("唯一持有者升级应成功: %v", err)
	}
	// 他人应被阻塞
	done := lockAsync(m, 2, "rowA", Shared, 2*time.Second)
	assertBlocked(t, done, 100*time.Millisecond, "升级后的X锁应阻塞他人S锁")
	m.Commit(1)
	assertResult(t, done, time.Second, nil, "释放后T2应获得S锁")
}

// 重入: 已持有的锁重复请求直接成功
func TestReentrantLock(t *testing.T) {
	m := NewManager(WoundWait)
	if err := m.Lock(1, "rowA", Exclusive, 0); err != nil {
		t.Fatal(err)
	}
	if err := m.Lock(1, "rowA", Exclusive, 0); err != nil {
		t.Fatalf("重入X锁应成功: %v", err)
	}
	if err := m.Lock(1, "rowA", Shared, 0); err != nil {
		t.Fatalf("持X请求S应成功: %v", err)
	}
}

// 无死锁压力测试: 多事务随机竞争, 最终全部完成(可能被中止重启)
func TestNoDeadlockUnderContention(t *testing.T) {
	m := NewManager(WoundWait)
	rows := []string{"r1", "r2", "r3"}
	done := make(chan bool, 8)
	for txn := uint64(1); txn <= 8; txn++ {
		go func(id uint64) {
			for attempt := 0; attempt < 20; attempt++ {
				m.Restart(id)
				ok := true
				for i := 0; i < 3; i++ {
					row := rows[(int(id)+i)%len(rows)]
					if err := m.Lock(id, row, Exclusive, 2*time.Second); err != nil {
						ok = false
						break
					}
				}
				if ok {
					m.Commit(id)
					done <- true
					return
				}
			}
			done <- false
		}(txn)
	}
	for i := 0; i < 8; i++ {
		select {
		case ok := <-done:
			if !ok {
				t.Fatal("事务重试次数耗尽, 疑似活锁")
			}
		case <-time.After(10 * time.Second):
			t.Fatal("系统疑似死锁: 10秒内未完成")
		}
	}
}
