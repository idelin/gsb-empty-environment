// Package zoning 是 SAS 扩展器固件的分区（zoning）控制模型。
//
// 它向两类调用方提供服务：
//   - SMP（Serial Management Protocol）请求：区域管理锁、口令校验、
//     许可表（permission table）的暂存与原子激活、物理端口分组变更；
//   - 连接请求：根据来源与目标物理端口所属的 zone group 做双向授权。
//
// 分区规则：
//   - 共 ZoneGroupCount(128) 个 zone group；
//   - 组 SuperZoneGroup(1) 为高权限组，可访问任意组；
//   - 组 RestrictedZoneGroup(0) 为受限组，只能访问组 1；
//   - 其余组之间的访问以许可表为准，连接要求两个方向同时放行。
package zoning

import (
	"crypto/subtle"
	"errors"
	"sync"
	"time"
)

const (
	// ZoneGroupCount 是本题约定的 zone group 总数。
	ZoneGroupCount = 128

	// SuperZoneGroup 是高权限 zone group，可访问所有组。
	SuperZoneGroup = 1

	// RestrictedZoneGroup 是受限 zone group，只能访问组 1。
	RestrictedZoneGroup = 0

	// MaxPasswordFailures 是连续口令错误多少次后进入锁定。
	MaxPasswordFailures = 5

	// DefaultLockTimeout 是区域管理锁的默认持有超时。
	DefaultLockTimeout = 30 * time.Second

	// DefaultLockoutDuration 是口令爆破锁定的默认时长。
	DefaultLockoutDuration = 60 * time.Second
)

var (
	// ErrZoneGroupOutOfRange 表示 zone group 编号超出 0..127。
	ErrZoneGroupOutOfRange = errors.New("zoning: zone group out of range")
	// ErrPhyNotFound 表示物理端口未登记或已断开。
	ErrPhyNotFound = errors.New("zoning: phy not found")
	// ErrUnknownPhy 表示 SMP 请求携带了未登记的物理端口标识。
	ErrUnknownPhy = errors.New("zoning: unknown phy")
	// ErrLockHeld 表示区域管理锁已被其他端口持有。
	ErrLockHeld = errors.New("zoning: zone management lock already held")
	// ErrNotLockOwner 表示调用方未持有区域管理锁。
	ErrNotLockOwner = errors.New("zoning: caller does not hold the zone management lock")
	// ErrPasswordMismatch 表示口令不匹配。
	ErrPasswordMismatch = errors.New("zoning: password mismatch")
	// ErrLockout 表示连续口令错误过多，管理接口被临时锁定。
	ErrLockout = errors.New("zoning: management lockout due to repeated password failures")
	// ErrReservedZoneGroup 表示试图改写组 0/组 1 的保留许可行。
	ErrReservedZoneGroup = errors.New("zoning: permission rows for zone group 0 and 1 are reserved")
	// ErrNoStagedConfig 表示尚无暂存配置可供激活。
	ErrNoStagedConfig = errors.New("zoning: no staged configuration to activate")
)

// Clock 抽象时间源，生产环境使用系统时钟，测试可注入假时钟。
type Clock interface {
	Now() time.Time
}

type systemClock struct{}

func (systemClock) Now() time.Time { return time.Now() }

// permissionMatrix 是 128x128 的定向许可表。
// permit[from][to] 为 true 时允许从 from 发起到 to 的访问。
// 数组为值类型，整体拷贝即可得到互不影响的快照/暂存副本。
type permissionMatrix [ZoneGroupCount][ZoneGroupCount]bool

// stagedConfig 是持有管理锁期间暂存、尚未生效的配置。
type stagedConfig struct {
	permit    permissionMatrix
	phyGroups map[int]int
}

// lockState 描述区域管理锁的当前状态，供观测与测试使用。
type lockState struct {
	held        bool
	ownerPhy    int
	acquiredAt  time.Time
	expiresAt   time.Time
	lockedUntil time.Time
	failures    int
}

// Options 是构造控制器的可选参数。
type Options struct {
	// Password 是区域管理锁口令，必须非空。
	Password string
	// LockTimeout 是锁的持有超时，零值使用 DefaultLockTimeout。
	LockTimeout time.Duration
	// LockoutDuration 是爆破锁定时长，零值使用 DefaultLockoutDuration。
	LockoutDuration time.Duration
	// Clock 为时间源，nil 时使用系统时钟。
	Clock Clock
}

// Controller 是分区控制器，供 SMP 请求与连接请求并发调用。
type Controller struct {
	mu sync.RWMutex

	password        []byte
	lockTimeout     time.Duration
	lockoutDuration time.Duration
	clock           Clock

	// permit 是当前生效的许可表；组 0/组 1 的行由规则保留。
	permit permissionMatrix
	// phyGroups 记录每个物理端口当前所属的 zone group。
	phyGroups map[int]int

	state  lockState
	staged *stagedConfig

	// generation 在每次原子激活后单调递增，用于验证表更新原子性。
	generation uint64
}

// New 创建分区控制器。初始许可表默认全拒绝，物理端口分组通过
// ProvisionPhy 在制造/初始化阶段登记，之后只能经加锁的 SMP 流程修改。
func New(opts Options) *Controller {
	if opts.Password == "" {
		panic("zoning: management password must not be empty")
	}
	lockTimeout := opts.LockTimeout
	if lockTimeout <= 0 {
		lockTimeout = DefaultLockTimeout
	}
	lockoutDuration := opts.LockoutDuration
	if lockoutDuration <= 0 {
		lockoutDuration = DefaultLockoutDuration
	}
	clock := opts.Clock
	if clock == nil {
		clock = systemClock{}
	}
	return &Controller{
		password:        []byte(opts.Password),
		lockTimeout:     lockTimeout,
		lockoutDuration: lockoutDuration,
		clock:           clock,
		phyGroups:       make(map[int]int),
	}
}

// validGroup 判断 zone group 编号是否合法。
func validGroup(group int) bool {
	return group >= 0 && group < ZoneGroupCount
}

// now 返回当前时间（注入时钟）。
func (c *Controller) now() time.Time { return c.clock.Now() }

// sweepLocked 在持锁状态下处理超时/锁定到期：
//   - 爆破锁定到期则清零错误计数；
//   - 管理锁到期则释放锁并丢弃未激活的暂存配置。
func (c *Controller) sweepLocked() {
	now := c.now()
	if !c.state.lockedUntil.IsZero() && !now.Before(c.state.lockedUntil) {
		c.state.lockedUntil = time.Time{}
		c.state.failures = 0
	}
	if c.state.held && !now.Before(c.state.expiresAt) {
		c.releaseLockLocked()
	}
}

// releaseLockLocked 释放管理锁并丢弃暂存配置。调用方必须持有 c.mu。
func (c *Controller) releaseLockLocked() {
	c.state.held = false
	c.state.ownerPhy = 0
	c.state.acquiredAt = time.Time{}
	c.state.expiresAt = time.Time{}
	c.staged = nil
}

// phyGroupLocked 解析物理端口所属组，未登记返回 ErrPhyNotFound。
func (c *Controller) phyGroupLocked(phy int) (int, error) {
	group, ok := c.phyGroups[phy]
	if !ok {
		return 0, ErrPhyNotFound
	}
	return group, nil
}

// passwordOK 使用恒定时间比较校验口令。
func (c *Controller) passwordOK(password string) bool {
	return subtle.ConstantTimeCompare([]byte(password), c.password) == 1
}

// directionAllowed 判断单个方向（from -> to）是否放行。
// 组 1 为高权限组可访问任意组；组 0 为受限组只能访问组 1；
// 其余组严格按许可表判定。
func (c *Controller) directionAllowed(from, to int) bool {
	switch from {
	case SuperZoneGroup:
		return true
	case RestrictedZoneGroup:
		return to == SuperZoneGroup
	default:
		return c.permit[from][to]
	}
}

// AllowDirection 返回指定 zone group 单方向访问是否被允许。
func (c *Controller) AllowDirection(from, to int) (bool, error) {
	if !validGroup(from) || !validGroup(to) {
		return false, ErrZoneGroupOutOfRange
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.directionAllowed(from, to), nil
}

// AllowConnection 处理连接请求：同时检查来源与目标的 zone group。
// 物理端口都必须已登记，并按以下顺序授权：
//   - 任一端为高权限组 1：组 1 可访问所有组，直接放行；
//   - 任一端为受限组 0：组 0 只能访问组 1（上一条已覆盖），否则拒绝；
//   - 其余组：许可表的两个方向都放行才允许连接。
func (c *Controller) AllowConnection(sourcePhy, destPhy int) (bool, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	sourceGroup, err := c.phyGroupLocked(sourcePhy)
	if err != nil {
		return false, err
	}
	destGroup, err := c.phyGroupLocked(destPhy)
	if err != nil {
		return false, err
	}

	if sourceGroup == SuperZoneGroup || destGroup == SuperZoneGroup {
		return true, nil
	}
	if sourceGroup == RestrictedZoneGroup || destGroup == RestrictedZoneGroup {
		return false, nil
	}
	return c.permit[sourceGroup][destGroup] && c.permit[destGroup][sourceGroup], nil
}

// ProvisionPhy 在初始化阶段登记物理端口及其 zone group。
// 该入口模拟制造配置，不属于 SMP 区域配置请求，因此不要求管理锁。
func (c *Controller) ProvisionPhy(phy, group int) error {
	if !validGroup(group) {
		return ErrZoneGroupOutOfRange
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.phyGroups[phy] = group
	if c.staged != nil {
		c.staged.phyGroups[phy] = group
	}
	return nil
}

// AcquireLock 处理 SMP 区域管理锁请求。
// 仅当锁空闲、未处于爆破锁定且口令匹配时才能获得锁；
// 已持有者重复获取会刷新持有超时；口令错误累计，达到阈值即锁定。
func (c *Controller) AcquireLock(phy int, password string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.sweepLocked()

	now := c.now()

	// 爆破锁定优先，避免锁定窗口内继续试探口令。
	if !c.state.lockedUntil.IsZero() {
		return ErrLockout
	}

	if c.state.held {
		if c.state.ownerPhy == phy && c.passwordOK(password) {
			c.state.expiresAt = now.Add(c.lockTimeout)
			return nil
		}
		if c.state.ownerPhy != phy {
			return ErrLockHeld
		}
		// 持有者口令错误同样计入失败次数。
		c.registerFailureLocked(now)
		return ErrPasswordMismatch
	}

	if !c.passwordOK(password) {
		c.registerFailureLocked(now)
		return ErrPasswordMismatch
	}

	c.state.held = true
	c.state.ownerPhy = phy
	c.state.acquiredAt = now
	c.state.expiresAt = now.Add(c.lockTimeout)
	c.state.failures = 0
	c.state.lockedUntil = time.Time{}
	return nil
}

// registerFailureLocked 记录一次口令错误，达到阈值进入爆破锁定。
func (c *Controller) registerFailureLocked(now time.Time) {
	c.state.failures++
	if c.state.failures >= MaxPasswordFailures {
		c.state.lockedUntil = now.Add(c.lockoutDuration)
	}
}

// ensureOwnerLocked 校验调用方持有锁且口令匹配，否则返回相应错误。
func (c *Controller) ensureOwnerLocked(phy int, password string) error {
	c.sweepLocked()
	if !c.state.held || c.state.ownerPhy != phy {
		return ErrNotLockOwner
	}
	if !c.passwordOK(password) {
		return ErrPasswordMismatch
	}
	return nil
}

// stagingLocked 返回当前暂存配置；首次修改时从生效配置复制一份，
// 保证暂存期内的变更不影响在线连接判定。
func (c *Controller) stagingLocked() *stagedConfig {
	if c.staged == nil {
		phyGroups := make(map[int]int, len(c.phyGroups))
		for phy, group := range c.phyGroups {
			phyGroups[phy] = group
		}
		c.staged = &stagedConfig{
			permit:    c.permit,
			phyGroups: phyGroups,
		}
	}
	return c.staged
}

// SetPermission 暂存一条许可表项（from -> to）。
// 必须持有管理锁且口令匹配；组 0/组 1 的许可行由固定规则保留，禁止改写。
func (c *Controller) SetPermission(phy int, password string, from, to int, allow bool) error {
	if !validGroup(from) || !validGroup(to) {
		return ErrZoneGroupOutOfRange
	}
	if from == RestrictedZoneGroup || from == SuperZoneGroup {
		return ErrReservedZoneGroup
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.ensureOwnerLocked(phy, password); err != nil {
		return err
	}
	c.stagingLocked().permit[from][to] = allow
	return nil
}

// SetPhyZoneGroup 暂存目标物理端口 targetPhy 的 zone group 归属变更。
// requesterPhy 必须是当前管理锁持有者且口令匹配。
func (c *Controller) SetPhyZoneGroup(requesterPhy, targetPhy int, password string, group int) error {
	if !validGroup(group) {
		return ErrZoneGroupOutOfRange
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.phyGroups[targetPhy]; !ok {
		return ErrUnknownPhy
	}
	if err := c.ensureOwnerLocked(requesterPhy, password); err != nil {
		return err
	}
	c.stagingLocked().phyGroups[targetPhy] = group
	return nil
}

// ActivateConfiguration 原子激活暂存配置：许可表与端口分组在同一临界区内
// 整体替换，任何连接请求看到的要么是旧配置、要么是新配置，不会读到半成品。
func (c *Controller) ActivateConfiguration(phy int, password string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.ensureOwnerLocked(phy, password); err != nil {
		return err
	}
	if c.staged == nil {
		return ErrNoStagedConfig
	}

	phyGroups := make(map[int]int, len(c.staged.phyGroups))
	for p, g := range c.staged.phyGroups {
		if !validGroup(g) {
			return ErrZoneGroupOutOfRange
		}
		phyGroups[p] = g
	}

	c.permit = c.staged.permit
	c.phyGroups = phyGroups
	c.staged = nil
	c.generation++
	return nil
}

// ReleaseLock 主动释放区域管理锁；未激活的暂存配置一并丢弃。
func (c *Controller) ReleaseLock(phy int, password string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.sweepLocked()
	if !c.state.held || c.state.ownerPhy != phy {
		return ErrNotLockOwner
	}
	if !c.passwordOK(password) {
		return ErrPasswordMismatch
	}
	c.releaseLockLocked()
	return nil
}

// HandleDisconnect 在物理端口断开时调用：若该端口持有管理锁则立即释放，
// 满足“锁在断开后释放”；同时移除该端口的分组登记。
func (c *Controller) HandleDisconnect(phy int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.state.held && c.state.ownerPhy == phy {
		c.releaseLockLocked()
	}
	delete(c.phyGroups, phy)
	if c.staged != nil {
		delete(c.staged.phyGroups, phy)
	}
}

// Snapshot 返回当前生效许可表的深拷贝与配置代次，用于原子性验证与审计。
// 拷贝在同一读临界区取得，与任何激活操作互不交错。
func (c *Controller) Snapshot() ([ZoneGroupCount][ZoneGroupCount]bool, uint64) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	var table [ZoneGroupCount][ZoneGroupCount]bool
	for from := range c.permit {
		copy(table[from][:], c.permit[from][:])
	}
	return table, c.generation
}

// PhyZoneGroup 返回物理端口当前生效的 zone group。
func (c *Controller) PhyZoneGroup(phy int) (int, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.phyGroupLocked(phy)
}

// LockInfo 是管理锁的只读观测视图。
type LockInfo struct {
	Held        bool
	OwnerPhy    int
	AcquiredAt  time.Time
	ExpiresAt   time.Time
	LockedUntil time.Time
	Failures    int
}

// LockInfoLocked 先按当前时钟处理超时，再返回管理锁状态快照。
func (c *Controller) LockInfoLocked() LockInfo {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.sweepLocked()
	return LockInfo{
		Held:        c.state.held,
		OwnerPhy:    c.state.ownerPhy,
		AcquiredAt:  c.state.acquiredAt,
		ExpiresAt:   c.state.expiresAt,
		LockedUntil: c.state.lockedUntil,
		Failures:    c.state.failures,
	}
}
