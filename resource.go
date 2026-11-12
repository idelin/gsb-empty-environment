package netsec

// OperationState 描述敏感操作从发起到终止的明确生命周期。
type OperationState int

const (
	// OpRunning 表示操作已开始且仍在执行。
	OpRunning OperationState = iota
	// OpCompleted 表示在 drain 策略下操作正常完成。
	OpCompleted
	// OpFailed 表示在 drain 策略下操作显式失败。
	OpFailed
	// OpAborted 表示资源受限时被 abort 策略强制中止。
	OpAborted
)

// String 返回操作状态的可读名称。
func (s OperationState) String() string {
	switch s {
	case OpRunning:
		return "running"
	case OpCompleted:
		return "completed"
	case OpFailed:
		return "failed"
	case OpAborted:
		return "aborted"
	default:
		return "unknown"
	}
}

// Operation 是一次已经开始的敏感操作的完整记录。
// 任何终止都是显式的：completed/failed/aborted 均保留时间戳，
// 不会因为根例外记录被处理而静默消失。
type Operation struct {
	ID         string
	ResourceID string
	StartedAt  Time

	state        OperationState
	terminatedAt Time
	// AbortNote 记录 abort 时触发的例外版本与原因，便于审计。
	abortNote string
}

// State 返回操作当前状态。
func (o *Operation) State() OperationState { return o.state }

// Terminated 报告操作是否已进入明确终止状态。
func (o *Operation) Terminated() bool { return o.state != OpRunning }

// TerminatedAt 返回终止时间；仍在运行时为零值。
func (o *Operation) TerminatedAt() Time { return o.terminatedAt }

// AbortNote 返回被中止时的审计说明；非 abort 终止为空。
func (o *Operation) AbortNote() string { return o.abortNote }

// Resource 是资源依赖视图的快照。
//
// ExceptionDeps 记录资源直接依赖的例外版本集合；
// UpstreamDeps 记录直接上游资源，传递依赖由索引沿边计算。
// 受限资源的 Causes 给出使其受限的全部失效版本
// （含菱形依赖去重后的结果）。
type Resource struct {
	ID            string
	ExceptionDeps []ExceptionVersion
	UpstreamDeps  []string
	Restricted    bool
	// TerminationPolicy 是当前作用于该资源的最严格终止策略；
	// 未受限时为零值 TerminateUnset，需结合 Restricted 判断。
	TerminationPolicy TerminationPolicy
	Causes            []ExceptionVersion
}
