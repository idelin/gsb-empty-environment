package leaseaudit

import "time"

// EventType 租约全生命周期中可被审计的事件类型。
type EventType string

const (
	EventAcquireRequest EventType = "ACQUIRE_REQUEST" // 节点申请租约
	EventAcquireGranted EventType = "ACQUIRE_GRANTED" // 仲裁器授予租约
	EventRenewRequest   EventType = "RENEW_REQUEST"   // 节点申请续期
	EventRenewed        EventType = "RENEWED"         // 续期成功
	EventHandoff        EventType = "HANDOFF"         // 租约移交
	EventRelease        EventType = "RELEASE"         // 持有者主动释放
	EventTimeout        EventType = "TIMEOUT"         // 租约超时回收
	EventRejected       EventType = "REJECTED"        // 仲裁器拒绝请求
	EventConflict       EventType = "CONFLICT"        // 检测到竞态冲突
	EventLockTransition EventType = "LOCK_TRANSITION" // 仲裁器锁状态变迁
)

// LockState 仲裁器内部锁的状态机。
type LockState string

const (
	StateIdle      LockState = "IDLE"      // 租约空闲
	StateGranting  LockState = "GRANTING"  // 正在仲裁授予（竞态窗口）
	StateHeld      LockState = "HELD"      // 租约被持有
	StateReleasing LockState = "RELEASING" // 正在释放/移交
)

// Event 一条结构化审计事件，携带逻辑时间戳与因果链指针。
type Event struct {
	Seq      uint64            // 黑匣子全局单调序号（到达次序）
	Lamport  uint64            // Lamport 逻辑时钟
	Vector   VectorClock       // 向量时钟快照（因果判据）
	WallTime time.Time         // 物理时钟（辅助观察，不作为因果依据）
	NodeID   string            // 事件主体（节点或仲裁器）
	LeaseID  string            // 租约标识
	Type     EventType         // 事件类型
	From     LockState         // 锁状态变迁：旧状态（仅 LOCK_TRANSITION）
	To       LockState         // 锁状态变迁：新状态（仅 LOCK_TRANSITION）
	Term     uint64            // 租约任期
	CausedBy []uint64          // 因果父事件的 Seq（证据链指针）
	Detail   string            // 人类可读描述
	Attrs    map[string]string // 附加属性
}
