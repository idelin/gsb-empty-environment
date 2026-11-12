package zoning

import (
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"
)

// fakeClock 是可手动推进的假时钟，避免测试依赖真实等待。
type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func newFakeClock() *fakeClock {
	return &fakeClock{now: time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC)}
}

func (f *fakeClock) Now() time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.now
}

func (f *fakeClock) advance(d time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.now = f.now.Add(d)
}

const testPassword = "sas-admin-secret"

func newTestController(t *testing.T) (*Controller, *fakeClock) {
	t.Helper()
	clock := newFakeClock()
	return New(Options{
		Password:        testPassword,
		LockTimeout:     time.Hour,
		LockoutDuration: 10 * time.Second,
		Clock:           clock,
	}), clock
}

func requireErrIs(t *testing.T, err, target error) {
	t.Helper()
	if !errors.Is(err, target) {
		t.Fatalf("期望错误 %v，实际 %v", target, err)
	}
}

// 公共 API 的边界与默认构造行为。
func TestPublicAPIBoundaries(t *testing.T) {
	// 默认时钟与默认超时时，控制器可直接使用系统时钟工作。
	c := New(Options{Password: testPassword})
	before := time.Now()
	got := c.now()
	after := time.Now()
	if got.Before(before) || got.After(after) {
		t.Fatalf("默认时钟应返回系统当前时间，got=%v", got)
	}

	func() {
		defer func() {
			if recover() == nil {
				t.Fatal("空口令构造应当 panic")
			}
		}()
		New(Options{Password: ""})
	}()

	// 越界 zone group 在各入口都被拒绝。
	requireErrIs(t, c.ProvisionPhy(1, ZoneGroupCount), ErrZoneGroupOutOfRange)
	requireErrIs(t, c.ProvisionPhy(1, -1), ErrZoneGroupOutOfRange)
	if _, err := c.AllowDirection(ZoneGroupCount, 0); !errors.Is(err, ErrZoneGroupOutOfRange) {
		t.Fatalf("AllowDirection 越界期望 ErrZoneGroupOutOfRange，实际 %v", err)
	}

	// 单方向授权规则：组 1 全通、组 0 仅到组 1、普通组默认拒绝。
	ok, err := c.AllowDirection(SuperZoneGroup, 90)
	if err != nil || !ok {
		t.Fatalf("组 1 单方向应放行，ok=%v err=%v", ok, err)
	}
	ok, err = c.AllowDirection(RestrictedZoneGroup, SuperZoneGroup)
	if err != nil || !ok {
		t.Fatalf("组 0 -> 组 1 应放行，ok=%v err=%v", ok, err)
	}
	ok, err = c.AllowDirection(RestrictedZoneGroup, 2)
	if err != nil || ok {
		t.Fatalf("组 0 -> 组 2 应拒绝，ok=%v err=%v", ok, err)
	}
	ok, err = c.AllowDirection(5, 6)
	if err != nil || ok {
		t.Fatalf("普通组默认许可应为拒绝，ok=%v err=%v", ok, err)
	}

	// 未登记端口的查询与断连行为。
	_, err = c.PhyZoneGroup(999)
	requireErrIs(t, err, ErrPhyNotFound)
	c.HandleDisconnect(999) // 断连未知端口应安全无副作用

	// 错误口令计数使用独立控制器，避免把本实例推入爆破锁定。
	lockoutC, _ := newTestController(t)
	for attempt := 0; attempt < MaxPasswordFailures-1; attempt++ {
		requireErrIs(t, lockoutC.AcquireLock(11, "bad"), ErrPasswordMismatch)
	}
	requireErrIs(t, lockoutC.AcquireLock(11, "bad"), ErrPasswordMismatch)
	requireErrIs(t, lockoutC.AcquireLock(11, testPassword), ErrLockout)

	// 本实例未受错误口令污染，可正常持锁；未登记端口返回 ErrUnknownPhy。
	if err := c.AcquireLock(11, testPassword); err != nil {
		t.Fatalf("AcquireLock 失败: %v", err)
	}
	requireErrIs(t, c.SetPhyZoneGroup(11, 404, testPassword, 2), ErrUnknownPhy)
	requireErrIs(t, c.SetPhyZoneGroup(11, 11, testPassword, ZoneGroupCount), ErrZoneGroupOutOfRange)

	// 无暂存内容时激活应明确报错；释放后再次释放则提示非持有者。
	requireErrIs(t, c.ActivateConfiguration(11, testPassword), ErrNoStagedConfig)
	if err := c.ReleaseLock(11, testPassword); err != nil {
		t.Fatalf("ReleaseLock 失败: %v", err)
	}
	requireErrIs(t, c.ReleaseLock(11, testPassword), ErrNotLockOwner)
}

func mustProvision(t *testing.T, c *Controller, phy, group int) {
	t.Helper()
	if err := c.ProvisionPhy(phy, group); err != nil {
		t.Fatalf("ProvisionPhy(%d,%d) 失败: %v", phy, group, err)
	}
}

func mustAllow(t *testing.T, c *Controller, src, dst int, want bool) {
	t.Helper()
	got, err := c.AllowConnection(src, dst)
	if err != nil {
		t.Fatalf("AllowConnection(%d,%d) 返回错误: %v", src, dst, err)
	}
	if got != want {
		t.Fatalf("AllowConnection(%d,%d) = %v, 期望 %v", src, dst, got, want)
	}
}

// 场景 1：越权连接。
// 验证高权限组 1 的连接能力、受限组 0 的限制，以及普通组的双向许可表检查。
func TestUnauthorizedConnection(t *testing.T) {
	c, _ := newTestController(t)

	// phy 10 -> 组 0（受限组），phy 11 -> 组 1（高权限组），
	// phy 20/21/30/31 -> 普通组 2/3，phy 90 -> 存储盘所在组 90。
	mustProvision(t, c, 10, RestrictedZoneGroup)
	mustProvision(t, c, 11, SuperZoneGroup)
	mustProvision(t, c, 20, 2)
	mustProvision(t, c, 21, 2)
	mustProvision(t, c, 30, 3)
	mustProvision(t, c, 90, 90)

	// 默认许可表全拒绝：普通组 2 -> 组 3、组 2 -> 存储盘均被拒。
	mustAllow(t, c, 20, 30, false)
	mustAllow(t, c, 20, 90, false)

	// 受限组 0 只能访问组 1，访问存储盘或普通组都被拒。
	mustAllow(t, c, 10, 11, true)
	mustAllow(t, c, 10, 90, false)
	mustAllow(t, c, 10, 20, false)

	// 高权限组 1 可访问所有组，包括存储盘；反向连接同样放行。
	mustAllow(t, c, 11, 90, true)
	mustAllow(t, c, 90, 11, true)
	mustAllow(t, c, 11, 20, true)

	// 未登记的物理端口不能参与连接。
	if _, err := c.AllowConnection(20, 404); !errors.Is(err, ErrPhyNotFound) {
		t.Fatalf("未登记端口期望 ErrPhyNotFound，实际 %v", err)
	}

	// 写入双向许可后，组 2 与组 3 才能互通；单向许可仍不足以建立连接。
	if err := c.AcquireLock(11, testPassword); err != nil {
		t.Fatalf("AcquireLock 失败: %v", err)
	}
	if err := c.SetPermission(11, testPassword, 2, 3, true); err != nil {
		t.Fatalf("SetPermission 失败: %v", err)
	}
	if err := c.ActivateConfiguration(11, testPassword); err != nil {
		t.Fatalf("ActivateConfiguration 失败: %v", err)
	}
	mustAllow(t, c, 20, 30, false) // 仅 2->3，缺少 3->2

	if err := c.AcquireLock(11, testPassword); err != nil {
		t.Fatalf("重新加锁失败: %v", err)
	}
	if err := c.SetPermission(11, testPassword, 3, 2, true); err != nil {
		t.Fatalf("SetPermission 失败: %v", err)
	}
	if err := c.ActivateConfiguration(11, testPassword); err != nil {
		t.Fatalf("ActivateConfiguration 失败: %v", err)
	}
	mustAllow(t, c, 20, 30, true)
	mustAllow(t, c, 30, 20, true)

	// 保留行（组 0/组 1）不允许通过许可表改写。
	if err := c.AcquireLock(11, testPassword); err != nil {
		t.Fatalf("重新加锁失败: %v", err)
	}
	requireErrIs(t, c.SetPermission(11, testPassword, SuperZoneGroup, 90, false), ErrReservedZoneGroup)
	requireErrIs(t, c.SetPermission(11, testPassword, RestrictedZoneGroup, 90, true), ErrReservedZoneGroup)
}

// 场景 2：无锁配置。
// 未持有管理锁或口令不匹配的 SMP 区域配置请求一律不得改写许可表。
func TestConfigurationRequiresLockAndPassword(t *testing.T) {
	c, _ := newTestController(t)
	mustProvision(t, c, 11, SuperZoneGroup)
	mustProvision(t, c, 20, 2)
	mustProvision(t, c, 30, 3)

	// 未加锁：所有配置入口都被拒绝。
	requireErrIs(t, c.SetPermission(11, testPassword, 2, 3, true), ErrNotLockOwner)
	requireErrIs(t, c.SetPhyZoneGroup(20, 20, testPassword, 3), ErrNotLockOwner)
	requireErrIs(t, c.ActivateConfiguration(11, testPassword), ErrNotLockOwner)

	// 加锁但口令错误：不能借锁持有者身份写入。
	if err := c.AcquireLock(11, testPassword); err != nil {
		t.Fatalf("AcquireLock 失败: %v", err)
	}
	requireErrIs(t, c.SetPermission(11, "wrong-password", 2, 3, true), ErrPasswordMismatch)

	// 其他端口既不是锁持有者，也不能抢占。
	requireErrIs(t, c.AcquireLock(20, testPassword), ErrLockHeld)
	requireErrIs(t, c.SetPermission(20, testPassword, 2, 3, true), ErrNotLockOwner)

	// 失败的写入没有产生任何暂存配置；即便后续合法激活也无内容可激活。
	requireErrIs(t, c.ActivateConfiguration(11, testPassword), ErrNoStagedConfig)

	// 合法流程：持锁 + 正确口令才能暂存并激活。
	if err := c.SetPermission(11, testPassword, 2, 3, true); err != nil {
		t.Fatalf("SetPermission 失败: %v", err)
	}
	if err := c.SetPermission(11, testPassword, 3, 2, true); err != nil {
		t.Fatalf("SetPermission 失败: %v", err)
	}
	if err := c.ActivateConfiguration(11, testPassword); err != nil {
		t.Fatalf("ActivateConfiguration 失败: %v", err)
	}
	mustAllow(t, c, 20, 30, true)
}

// 场景 3：口令爆破。
// 连续 MaxPasswordFailures 次口令错误后进入锁定，锁定窗口内即使口令正确也拒绝；
// 锁定期满后错误计数清零，正确口令可重新获得管理锁。
func TestPasswordBruteForceLockout(t *testing.T) {
	c, clock := newTestController(t)

	for attempt := 1; attempt < MaxPasswordFailures; attempt++ {
		err := c.AcquireLock(11, fmt.Sprintf("guess-%d", attempt))
		requireErrIs(t, err, ErrPasswordMismatch)
		if info := c.LockInfoLocked(); info.Failures != attempt || info.Held {
			t.Fatalf("第 %d 次错误后状态异常: %+v", attempt, info)
		}
	}

	// 第 MaxPasswordFailures 次错误触发爆破锁定。
	requireErrIs(t, c.AcquireLock(11, "last-wrong-guess"), ErrPasswordMismatch)
	info := c.LockInfoLocked()
	if info.Held || info.LockedUntil.IsZero() {
		t.Fatalf("触发锁定后状态异常: %+v", info)
	}

	// 锁定窗口内，即便口令正确也一律拒绝，防止继续试探。
	requireErrIs(t, c.AcquireLock(11, testPassword), ErrLockout)

	// 锁定窗口内的配置请求同样被挡在锁外。
	requireErrIs(t, c.SetPermission(11, testPassword, 2, 3, true), ErrNotLockOwner)

	// 锁定期满，错误计数清零，正确口令可以加锁。
	clock.advance(10 * time.Second)
	if err := c.AcquireLock(11, testPassword); err != nil {
		t.Fatalf("锁定解除后正确口令应能加锁，实际: %v", err)
	}
	if info := c.LockInfoLocked(); !info.Held || info.Failures != 0 {
		t.Fatalf("重新加锁后状态异常: %+v", info)
	}
}

// 场景 4：锁超时与断连释放。
// 管理锁在持有超时后自动释放并丢弃暂存配置；端口断连也会立即释放其持有的锁。
func TestLockTimeoutAndDisconnect(t *testing.T) {
	clock := newFakeClock()
	c := New(Options{
		Password:        testPassword,
		LockTimeout:     time.Second,
		LockoutDuration: 10 * time.Second,
		Clock:           clock,
	})
	mustProvision(t, c, 11, SuperZoneGroup)

	// 超时路径：暂存但未激活的配置随超时一起丢弃。
	if err := c.AcquireLock(11, testPassword); err != nil {
		t.Fatalf("AcquireLock 失败: %v", err)
	}
	if err := c.SetPermission(11, testPassword, 5, 6, true); err != nil {
		t.Fatalf("SetPermission 失败: %v", err)
	}
	clock.advance(time.Second + time.Millisecond)

	info := c.LockInfoLocked()
	if info.Held {
		t.Fatalf("超时后管理锁应已释放: %+v", info)
	}
	// 超时释放后其他端口可立即取得锁；旧暂存已丢弃，激活无内容。
	if err := c.AcquireLock(20, testPassword); err != nil {
		t.Fatalf("超时后其他端口应能加锁，实际: %v", err)
	}
	requireErrIs(t, c.ActivateConfiguration(20, testPassword), ErrNoStagedConfig)
	if err := c.ReleaseLock(20, testPassword); err != nil {
		t.Fatalf("ReleaseLock 失败: %v", err)
	}

	// 断连路径：持有者端口断开，锁立即释放。
	if err := c.AcquireLock(11, testPassword); err != nil {
		t.Fatalf("AcquireLock 失败: %v", err)
	}
	if err := c.SetPermission(11, testPassword, 5, 6, true); err != nil {
		t.Fatalf("SetPermission 失败: %v", err)
	}
	c.HandleDisconnect(11)
	if info := c.LockInfoLocked(); info.Held {
		t.Fatalf("断连后管理锁应已释放: %+v", info)
	}

	// 断连端口的分组登记已移除，不能再作为连接端点。
	if _, err := c.PhyZoneGroup(11); !errors.Is(err, ErrPhyNotFound) {
		t.Fatalf("断连端口期望 ErrPhyNotFound，实际 %v", err)
	}
	// 其他端口可正常接管管理锁。
	if err := c.AcquireLock(20, testPassword); err != nil {
		t.Fatalf("断连释放后其他端口应能加锁，实际: %v", err)
	}
}

// 物理端口的 zone group 变更同样走“持锁暂存 + 原子激活”，
// 激活前连接判定使用旧分组，激活后才切换到新分组。
func TestPhyZoneGroupChangeAtomic(t *testing.T) {
	clock := newFakeClock()
	c := New(Options{
		Password:        testPassword,
		LockTimeout:     time.Hour,
		LockoutDuration: 10 * time.Second,
		Clock:           clock,
	})
	mustProvision(t, c, 11, SuperZoneGroup)
	mustProvision(t, c, 20, 2)
	mustProvision(t, c, 30, 3)

	// 先让组 2/组 3 双向互通。
	if err := c.AcquireLock(11, testPassword); err != nil {
		t.Fatalf("AcquireLock 失败: %v", err)
	}
	if err := c.SetPermission(11, testPassword, 2, 3, true); err != nil {
		t.Fatalf("SetPermission 失败: %v", err)
	}
	if err := c.SetPermission(11, testPassword, 3, 2, true); err != nil {
		t.Fatalf("SetPermission 失败: %v", err)
	}
	if err := c.ActivateConfiguration(11, testPassword); err != nil {
		t.Fatalf("ActivateConfiguration 失败: %v", err)
	}
	mustAllow(t, c, 20, 30, true)

	// 把 phy 20 暂存改到组 4；激活前仍按组 2 判定。
	if err := c.AcquireLock(11, testPassword); err != nil {
		t.Fatalf("重新加锁失败: %v", err)
	}
	if err := c.SetPhyZoneGroup(11, 20, testPassword, 4); err != nil {
		t.Fatalf("SetPhyZoneGroup 失败: %v", err)
	}
	mustAllow(t, c, 20, 30, true)
	if group, err := c.PhyZoneGroup(20); err != nil || group != 2 {
		t.Fatalf("激活前分组应仍为 2，实际 group=%d err=%v", group, err)
	}

	if err := c.ActivateConfiguration(11, testPassword); err != nil {
		t.Fatalf("ActivateConfiguration 失败: %v", err)
	}
	if group, err := c.PhyZoneGroup(20); err != nil || group != 4 {
		t.Fatalf("激活后分组应为 4，实际 group=%d err=%v", group, err)
	}
	// 组 4 与组 3 没有许可，连接被拒，证明高权限端口无法借旧分组越权。
	mustAllow(t, c, 20, 30, false)
}

// 场景 5：表更新原子性。
// 暂存期间在线连接判定保持旧配置；激活在单个临界区内整体替换，
// 快照与代次只能观察到“旧整表”或“新整表”，不会读到部分更新。
func TestAtomicTableUpdate(t *testing.T) {
	c, _ := newTestController(t)
	mustProvision(t, c, 20, 2)
	mustProvision(t, c, 30, 3)
	mustProvision(t, c, 40, 4)

	oldTable, gen0 := c.Snapshot()
	if gen0 != 0 {
		t.Fatalf("初始代次应为 0，实际 %d", gen0)
	}

	if err := c.AcquireLock(11, testPassword); err != nil {
		t.Fatalf("AcquireLock 失败: %v", err)
	}
	// 在暂存副本上写入多项变更，其中包含两个方向、多组关系。
	updates := []struct{ from, to int }{
		{2, 3}, {3, 2}, {2, 4}, {4, 2}, {3, 4}, {4, 3},
	}
	for _, u := range updates {
		if err := c.SetPermission(11, testPassword, u.from, u.to, true); err != nil {
			t.Fatalf("SetPermission(%d,%d) 失败: %v", u.from, u.to, err)
		}
	}

	// 激活前：连接判定与快照都必须仍是旧配置。
	mustAllow(t, c, 20, 30, false)
	mustAllow(t, c, 20, 40, false)
	if table, gen := c.Snapshot(); gen != gen0 || table != oldTable {
		t.Fatalf("暂存期间生效表不得改变，gen=%d", gen)
	}

	if err := c.ActivateConfiguration(11, testPassword); err != nil {
		t.Fatalf("ActivateConfiguration 失败: %v", err)
	}

	// 激活后：所有写入在同一代次一起生效。
	mustAllow(t, c, 20, 30, true)
	mustAllow(t, c, 20, 40, true)
	mustAllow(t, c, 30, 40, true)
	newTable, gen1 := c.Snapshot()
	if gen1 != gen0+1 {
		t.Fatalf("激活后代次应为 %d，实际 %d", gen0+1, gen1)
	}
	for _, u := range updates {
		if !newTable[u.from][u.to] {
			t.Fatalf("新表缺少许可项 %d -> %d", u.from, u.to)
		}
	}
	if newTable == oldTable {
		t.Fatal("激活后许可表应发生变化")
	}

	// 第二次整表替换：只改回一个方向，验证不会残留半成品状态。
	if err := c.AcquireLock(11, testPassword); err != nil {
		t.Fatalf("重新加锁失败: %v", err)
	}
	if err := c.SetPermission(11, testPassword, 2, 3, false); err != nil {
		t.Fatalf("SetPermission 失败: %v", err)
	}
	if err := c.ActivateConfiguration(11, testPassword); err != nil {
		t.Fatalf("ActivateConfiguration 失败: %v", err)
	}
	mustAllow(t, c, 20, 30, false) // 2->3 被撤销，双向不再同时满足
	mustAllow(t, c, 30, 20, false)
	if _, gen := c.Snapshot(); gen != gen1+1 {
		t.Fatalf("第二次激活后代次应为 %d，实际 %d", gen1+1, gen)
	}
}

// 并发压测：激活与连接判定并发执行时，任一连接结果必须能被某一整代配置解释，
// 不会出现撕裂读取（例如同一代次内只看到部分表项）。
func TestConcurrentActivationAndConnection(t *testing.T) {
	c, _ := newTestController(t)
	mustProvision(t, c, 20, 2)
	mustProvision(t, c, 30, 3)

	const rounds = 200
	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		for i := 0; i < rounds; i++ {
			allow := i%2 == 0
			if err := c.AcquireLock(11, testPassword); err != nil {
				t.Errorf("AcquireLock 失败: %v", err)
				return
			}
			if err := c.SetPermission(11, testPassword, 2, 3, allow); err != nil {
				t.Errorf("SetPermission 失败: %v", err)
				return
			}
			if err := c.SetPermission(11, testPassword, 3, 2, allow); err != nil {
				t.Errorf("SetPermission 失败: %v", err)
				return
			}
			if err := c.ActivateConfiguration(11, testPassword); err != nil {
				t.Errorf("ActivateConfiguration 失败: %v", err)
				return
			}
		}
	}()

	go func() {
		defer wg.Done()
		for i := 0; i < rounds; i++ {
			if _, err := c.AllowConnection(20, 30); err != nil {
				t.Errorf("AllowConnection 失败: %v", err)
				return
			}
			// 并发激活下，连接判定不得报错；每次快照必须是自洽的整代配置。
			table, gen := c.Snapshot()
			consistent := table[2][3] == table[3][2]
			if !consistent {
				t.Errorf("代次 %d 出现撕裂表: 2->3=%v 3->2=%v", gen, table[2][3], table[3][2])
				return
			}
		}
	}()

	wg.Wait()
}
