// Package saga 实现长事务补偿（Saga）与状态机持久化引擎。
//
// 长事务被切分为若干步骤（Step），每个步骤包含一个正向动作（Action）
// 与一个逆向补偿动作（Compensate）。步骤可以同步完成，也可以返回
// ErrWait 进入等待状态，挂起直到外部系统回调（Complete/Fail）或
// 超时监控器判定等待超时。每一次状态迁移都会先落盘再推进，
// 保证断电重启后可以从持久化状态精确恢复，不重跑已完成步骤、
// 不遗漏未完成步骤。
package saga

import (
	"errors"
	"time"
)

// ErrWait 由 Step.Action 返回，表示该步骤已发起外部异步调用，
// 事务应进入等待状态，直到外部回调或超时。
var ErrWait = errors.New("saga: step awaits external completion")

// Status 是实例或步骤的状态机状态。
type Status string

const (
	StatusPending      Status = "pending"      // 待执行
	StatusRunning      Status = "running"      // 正在执行
	StatusWaiting      Status = "waiting"      // 等待外部异步结果
	StatusCompleted    Status = "completed"    // 成功完成
	StatusFailed       Status = "failed"       // 失败（含超时、外部拒绝）
	StatusCompensating Status = "compensating" // 正在逆向补偿
	StatusCompensated  Status = "compensated"  // 补偿完成（事务终态）
)

// StepState 是单个步骤的持久化状态。
type StepState struct {
	Name        string    `json:"name"`
	Status      Status    `json:"status"`
	Attempts    int       `json:"attempts"`
	Result      string    `json:"result,omitempty"`
	Err         string    `json:"err,omitempty"`
	Deadline    time.Time `json:"deadline,omitempty"` // waiting 状态的超时截止时刻
	StartedAt   time.Time `json:"started_at,omitempty"`
	CompletedAt time.Time `json:"completed_at,omitempty"`
	Compensated bool      `json:"compensated,omitempty"`
}

// Instance 是一个长事务实例的完整持久化状态。
type Instance struct {
	ID        string            `json:"id"`
	Saga      string            `json:"saga"`
	Status    Status            `json:"status"`
	Steps     []StepState       `json:"steps"`
	Data      map[string]string `json:"data,omitempty"` // 步骤间共享的业务数据，随状态一起持久化
	CreatedAt time.Time         `json:"created_at"`
	UpdatedAt time.Time         `json:"updated_at"`
}

// Terminal 报告实例是否已到达终态。
func (in *Instance) Terminal() bool {
	return in.Status == StatusCompleted || in.Status == StatusCompensated
}

// Event 是审计轨迹中的一条状态迁移记录，以 JSONL 追加写入审计日志。
type Event struct {
	Time       time.Time `json:"time"`
	InstanceID string    `json:"instance_id"`
	Type       string    `json:"type"`
	Step       string    `json:"step,omitempty"`
	Detail     string    `json:"detail,omitempty"`
}

// Context 传给步骤动作与补偿动作的上下文。
type Context struct {
	InstanceID string
	Step       string
	Data       map[string]string // 跨步骤共享数据，随实例持久化
}

// Step 定义长事务中的一个步骤。
type Step struct {
	Name string
	// Timeout 仅当 Action 返回 ErrWait 时生效：等待外部结果的最长时间，
	// 超时后由监控器触发逆向补偿。0 表示无限等待。
	Timeout    time.Duration
	Action     func(ctx *Context) error // 正向动作；返回 ErrWait 进入等待
	Compensate func(ctx *Context) error // 逆向补偿动作，可为 nil（无需补偿）
}

// SagaDef 是一个长事务的定义：一组按序执行的步骤。
type SagaDef struct {
	Name  string
	Steps []Step
}
