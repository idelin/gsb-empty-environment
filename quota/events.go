// Package quota 提供磁盘配额预留库：先按预估写入容量预留，
// 压缩完成后按实际大小结算；支持租户与全局两级配额、
// 幂等的预留/结算/取消、共享物理块的单归属计费与原子归属迁移，
// 并通过事件账本回放复算历史峰值用量。
package quota

import "time"

// EventType 账本事件类型。
type EventType string

const (
	EventReserved    EventType = "reserved"    // 预留成功
	EventSettled     EventType = "settled"     // 结算成功（按实际大小计费）
	EventRejected    EventType = "rejected"    // 结算超额，预留释放，文件不发布
	EventCanceled    EventType = "canceled"    // 主动取消预留
	EventAborted     EventType = "aborted"     // 压缩失败等原因中止
	EventTransferred EventType = "transferred" // 物理块归属迁移（原子）
	EventQuotaSet    EventType = "quota_set"   // 配额调整（租户或全局）
)

// Event 是账本中的一条不可变记录。所有状态变更都先落账本再生效，
// 峰值等派生指标可通过回放账本复算。
type Event struct {
	Seq           uint64    // 全局单调递增序号
	Time          time.Time // 事件发生时间
	Type          EventType // 事件类型
	Tenant        string    // 相关租户（全局事件为空）
	ReservationID string    // 相关预留单号（配额事件为空）
	ReservedBytes int64     // 预留量（reserved 事件）
	ActualBytes   int64     // 实际结算量（settled 事件）
	BlockID       string    // 物理块 ID（settled/transferred 事件）
	FromTenant    string    // 归属迁出租户（transferred 事件）
	ToTenant      string    // 归属迁入租户（transferred 事件）
	QuotaBytes    int64     // 新配额（quota_set 事件，-1 表示不限）
}

// Unlimited 表示不限配额。
const Unlimited int64 = -1
