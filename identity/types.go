// Package identity 为 Go 监视宿主提供可导入的身份归属分析。
//
// 追踪记录只给出每个线程内部顺序与少量跨线程屏障，因此本包不假设日志写入
// 顺序等于身份切换顺序，而是在事件偏序上做有界的显式状态空间分析：枚举
// “可执行事件集合”这一层的抽象状态（不逐个枚举拓扑排序），对每个受保护
// 读取给出三类判定，并区分非法迁移与真正的违规证据。
package identity

import "errors"

// Kind 是事件类型。
type Kind uint8

const (
	// EnterRequest 进入请求，压入请求身份帧。
	EnterRequest Kind = iota
	// Borrow 显式借用，压入普通借用帧（LIFO）。
	Borrow
	// Restore 恢复（归还借用），只能弹出当前线程栈顶的借用帧。
	Restore
	// Spawn 派生异步回调，捕获当前有效身份快照，写入队列槽句柄。
	Spawn
	// Suspend 挂起，把当前栈顶身份帧移入本线程挂起栈。
	Suspend
	// ResumeHandle 指定句柄恢复，消费句柄并安装捕获时的身份快照。
	ResumeHandle
	// ProtectedRead 受保护读取，按有效身份与策略比较得到符合/违规。
	ProtectedRead
	// Exit 线程显式退出；链尾也会隐式执行同样的退出校验。
	Exit
)

// Token 是绑定租户与请求代次的身份令牌。
type Token struct {
	Tenant     string
	Generation int64
}

// Policy 是受保护读取要求的身份。
type Policy struct {
	Tenant     string
	Generation int64
}

// Event 是偏序中的一个事件。同一 Chain 内按切片顺序定全序，After 给出
// 额外的跨线程屏障前驱。BorrowID 仅 Borrow/Restore 使用，-1 表示不指定；
// Slot/HandleGeneration 仅 Spawn/ResumeHandle 使用。
type Event struct {
	ID               int
	Chain            int
	Thread           int
	Kind             Kind
	Token            Token
	Slot             int
	HandleGeneration int64
	BorrowID         int
	Policy           Policy
	After            []int
}

// Input 是分析输入，事件数不得超过 36。
type Input struct {
	Events []Event
}

// Budget 限定分析工作量；零值字段使用默认预算。
type Budget struct {
	MaxStates      int
	MaxTransitions int
}

// Outcome 是受保护读取在某个合法历史下的结果。
type Outcome uint8

const (
	// Conform 有效身份与策略一致。
	Conform Outcome = iota
	// Violation 无有效身份或身份与策略不一致。
	Violation
)

// Class 是受保护读取在全部合法历史上的分类。
type Class uint8

const (
	// AllConform 所有可达合法历史均符合。
	AllConform Class = iota
	// AllViolate 所有可达合法历史均违规。
	AllViolate
	// Mixed 既有符合也有违规的合法历史。
	Mixed
	// Unknown 预算耗尽或不存在合法历史，无法给出三分类结论。
	Unknown
	// Unreachable 读取在任何合法前缀中都不可达。
	Unreachable
)

// 非法迁移原因。非法事件的交错只用于审计，永远不计入违规证据。
const (
	ReasonEmptyStack       = "active identity stack is empty"
	ReasonLIFO             = "restore target is not the top borrow frame"
	ReasonForeignBorrow    = "borrow returned on a different thread"
	ReasonUnknownBorrow    = "restore names an unknown borrow"
	ReasonDuplicateBorrow  = "duplicate borrow id"
	ReasonStaleHandle      = "handle slot/generation is stale or unknown"
	ReasonConsumedHandle   = "handle has already been consumed"
	ReasonBorrowsAtExit    = "ordinary borrow stack is not empty at thread exit"
	ReasonNotProtectedRead = "event is not a protected read"
)

// IllegalAttempt 记录一次被拒绝的非法迁移，用于审计而非违规判定。
type IllegalAttempt struct {
	EventID int
	Reason  string
}

// ReadResult 是单个受保护读取的分析结论与最短前缀。
type ReadResult struct {
	EventID int
	Class   Class
	// ReachableConform/ReachableViolate 表示对应事实在合法前缀中是否可达。
	ReachableConform bool
	ReachableViolate bool
	// ConformPrefix/ViolatePrefix 是对应可达事实的最短合法前缀（事件 ID 序列）。
	// 当存在完整合法历史时，前缀保证可扩展为完整合法历史。
	ConformPrefix []int
	ViolatePrefix []int
	Reason        string
}

// CoverageCertificate 是与某个受保护读取严格绑定的覆盖证明。它列出该读取
// 被执行时所有可达抽象状态的指纹；复核时独立重跑分析，而不是接受一个恰好
// 通过的调度。
type CoverageCertificate struct {
	ModelFingerprint  string
	ReadID            int
	Outcome           Outcome
	Exhaustive        bool
	StateFingerprints []string
}

// AnalysisResult 是分析结果。
type AnalysisResult struct {
	ModelFingerprint string
	Exhaustive       bool
	BudgetCutoff     bool
	// HasLegalHistory 表示是否存在执行完全部事件的合法历史。
	HasLegalHistory  bool
	StatesExplored   int
	TransitionsTried int
	Reads            map[int]*ReadResult
	Coverage         map[int]*CoverageCertificate
	IllegalAttempts  []IllegalAttempt
	// StatesByExecutedSet 把已执行事件位集映射到该集合上出现过的全部抽象状态
	// 指纹。
	StatesByExecutedSet map[uint64][]string
	// ReadStates 按读取事件 ID 收集该读取在各合法交错中刚执行完时的观测，
	// 用于验证同一读取在不同交错中的身份栈分歧。
	ReadStates map[int][]ReadObservation

	input   Input
	budget  Budget
	liveFps map[string]struct{}
	allFps  map[string]struct{}
}

// ReadObservation 是一次受保护读取在某条合法交错中的状态观测。
type ReadObservation struct {
	ExecutedSet uint64
	Fingerprint string
}

const (
	maxEvents         = 36
	defaultMaxStates  = 200000
	defaultMaxTrans   = 2000000
	maxIllegalSamples = 64
)

var (
	errTooManyEvents = errors.New("identity: at most 36 events are supported")
	errNoEvents      = errors.New("identity: input contains no events")
)
