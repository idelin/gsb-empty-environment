package netsec

import "errors"

// Kind 标识一次诊断事件的具体类型。
type Kind int

const (
	// KindWait：某一方在一个资源集合上等待，集合内任一资源可用即解除（择一满足）。
	KindWait Kind = iota + 1
	// KindProduce：生产一个资源，可解除正在等待它的事件。
	KindProduce
	// KindCancel：取消此前登记的某个等待事件。
	KindCancel
	// KindSend：一次真实发生的消息发送，仅表示该消息此后可被接收。
	KindSend
	// KindReceive：一次真实发生的消息接收；消息必须先由真实发送产生，不补造消息。
	KindReceive
)

// String 返回事件类型的稳定可读名称，供诊断宿主输出。
func (k Kind) String() string {
	switch k {
	case KindWait:
		return "wait"
	case KindProduce:
		return "produce"
	case KindCancel:
		return "cancel"
	case KindSend:
		return "send"
	case KindReceive:
		return "receive"
	default:
		return "unknown"
	}
}

// Event 是协议运行中按真实发生顺序记录的一条事件。
//
// 资源一律以字符串标识，等待事件通过 Resources 给出候选集合，
// 集合内任意一个资源被生产（Produce 或 Receive 产出）即可解除该等待，
// 从而支持“多个事件择一满足”的依赖语义。
//
// 字段约定：
//   - Wait：Actor 等待，Resources 为候选资源集合，Ref/Produces 为空。
//   - Produce：Actor 生产，Produces 为被生产资源（单个）。
//   - Cancel：Actor 取消 Ref 指向的等待事件。
//   - Send：Actor 真实发送 Ref 标识的消息，消息进入在途/可用集合。
//   - Receive：Actor 真实接收 Ref 消息，并可通过 Produces 附带产出资源。
//
// 事件的先后顺序完全由切片下标决定，包内不会重排，也不会合成任何事件。
type Event struct {
	Kind       Kind
	Actor      string
	Resources  []string // Wait 使用：任一资源可用即满足
	Produces   string   // Produce / Receive 使用
	Ref        string   // Cancel 指向的等待事件标识；Send/Receive 的消息标识
	Annotation string   // 宿主侧的自由备注（可选），原样保留在结果中
}

// Step 是一次具体重放中单个事件的处置记录，用于把核对解释落到逐条事件上。
type Step struct {
	Index   int // 事件在原始事件切片中的下标
	Kind    Kind
	Actor   string
	Applied bool   // true=被执行；false=因所属方已阻塞而跳过
	Outcome string // 人类可读的处置说明
}

// ReplayReport 是对某个事件子集做一次具体重放的完整结果。
type ReplayReport struct {
	// Included 是本次重放包含的原始事件下标，按真实顺序排列。
	Included []int
	// Steps 按真实顺序记录每个被选事件的处置情况。
	Steps []Step
	// Pending 是重放结束时仍未解除的等待事件原始下标（含卡死的接收）。
	Pending []int
	// Skipped 是因所属方已阻塞而无法执行、被跳过的事件原始下标。
	// 复现完整卡死状态时，这些真实的后续发送/接收也必须保持被阻塞。
	Skipped []int
	// BlockedActors 是因等待未解除而无法继续推进的参与方。
	BlockedActors []string
	// AvailableResources 是重放结束时已经可用的资源集合。
	AvailableResources []string
	// AvailableMessages 是已经真实发送、此后可被接收的消息标识集合。
	AvailableMessages []string
}

// Dependency 描述一个等待为何无法解除，即等待环中的一条边。
type Dependency struct {
	WaitIndex    int    // 处于等待中的事件下标
	WaitActor    string // 等待方
	Resources    []string
	Kind         string // "resource"：等待资源；"message"：等待消息到达
	BlockedOnRef string // 消息等待时，缺失的消息标识
	BlockedBy    []int  // 直接导致其无法推进的事件下标（候选等待/缺失的前置事件）
	Note         string
}

// RemovalCheck 记录从最小子图中移除一个事件后的具体重放核对结果。
type RemovalCheck struct {
	RemovedIndex int   // 被移除的事件原始下标
	StillStuck   bool  // 移除后是否仍复现完整卡死状态（pending 与 skipped 均一致）
	Released     []int // 移除后得以解除的等待事件下标
	SkippedLost  []int // 移除后不再被阻塞跳过的事件下标（卡死状态被破坏的证据）
	Note         string
}

// Core 是一个通过全子集枚举与具体重放核对得到的最小事件子图。
type Core struct {
	// Events 是子图包含的原始事件下标，严格按真实顺序排列。
	Events []int
	// Dependencies 是该子图复现出来的等待依赖边。
	Dependencies []Dependency
	// Replay 是对该子图本身的具体重放记录，用于核对“全部当前等待仍无法解除”。
	Replay ReplayReport
	// RemovalChecks 对每个非锚点事件给出移除重放：移除后必须至少解除一个等待，
	// 以此具体证明该事件在子图中的必要性。
	RemovalChecks []RemovalCheck
	// Trivial 为 true 表示仅靠等待锚点本身即可复现卡死（等待的资源从未被生产），
	// 此时不存在可裁剪的消息依赖边，最小子图就是等待事件集合本身。
	Trivial bool
}

// Options 控制 Analyze 的枚举与输出规模。
type Options struct {
	// MaxCores 限制返回的最小子图数量；0 表示全部返回。
	MaxCores int
}

// Report 是 Analyze 的诊断结果。
type Report struct {
	// AnchorWaits 是完整轨迹重放后仍未解除的全部当前等待（分析锚点）。
	AnchorWaits []int
	// TotalSubsets 是枚举核对过的子集总数（含空集）。
	TotalSubsets int
	// StuckSubsets 是其中仍复现完整卡死状态（pending 与 skipped 均一致）的子集数。
	StuckSubsets int
	// ReplayCount 是实际执行的具体重放次数。
	ReplayCount int
	// MinimalSize 是最小卡死子图包含的事件数。
	MinimalSize int
	// Cores 是全部（或受 MaxCores 限制的）最小卡死子图。
	Cores []Core
	// Ring 是在完整轨迹上闭合的等待环（消息/资源依赖闭环），按真实顺序给出。
	// Trivial 场景下 Ring 可能为空。
	Ring []int
	// RingClosed 表明 Ring 是否构成依赖闭环。
	RingClosed bool
	// FullReplay 是对完整候选轨迹的基准重放。
	FullReplay ReplayReport
}

// MaxCandidates 是单次分析允许的候选事件上限。
const MaxCandidates = 20

var (
	// ErrTooManyCandidates：候选事件超过 MaxCandidates，全子集枚举不可接受。
	ErrTooManyCandidates = errors.New("netsec: candidate events exceed MaxCandidates (20)")
	// ErrNoEvents：传入的事件切片为空。
	ErrNoEvents = errors.New("netsec: no events provided")
	// ErrNoPendingWaits：完整轨迹中不存在仍未解除的等待，没有等待环可定位。
	ErrNoPendingWaits = errors.New("netsec: no pending waits remain in the full trace")
)
