package taskpkg

// maxEvents 是检测器接受的普通事件数量上限（不含撤销事件）。
const maxEvents = 20

// Kind 标识宿主上报事件的类型。
type Kind int

const (
	// KindUnknown 是零值，宿主不得显式使用。
	KindUnknown Kind = iota
	// KindTokenRead 表示主体在设备上读取（窃取）了一枚令牌。
	KindTokenRead
	// KindTokenAuth 表示主体在设备上以某枚令牌向服务发起认证。
	KindTokenAuth
	// KindProtectedOp 表示某次认证会话执行了受保护操作。
	KindProtectedOp
)

// String 返回事件类型的可读名称。
func (k Kind) String() string {
	switch k {
	case KindTokenRead:
		return "token-read"
	case KindTokenAuth:
		return "token-auth"
	case KindProtectedOp:
		return "protected-op"
	default:
		return "unknown"
	}
}

// TokenID 是令牌的完整身份：只有签发者、摘要与代次三者全部相同，
// 才被视为同一枚令牌。代次不同意味着换发，新旧令牌不可互相顶替。
type TokenID struct {
	Issuer     string // 签发者
	Digest     string // 令牌摘要
	Generation uint64 // 代次
}

// Instance 是带可信实例号的主体或设备标识。实例号由受信任来源分配，
// 同名但实例号不同的两个实例是不同主体/设备，不能被拼成一条链。
type Instance struct {
	Name     string
	Instance uint64
}

// Event 是宿主上报的单个事件。After 给出偏序约束：当前事件发生在
// 列表中每个事件之后（After 里的元素是其它事件的 ID）。
type Event struct {
	ID      string
	Kind    Kind
	Token   TokenID
	Subject Instance
	Device  Instance
	// Session 把受保护操作绑定到具体的某次认证；非操作事件可为空。
	Session string
	After   []string
}

// Revocation 表示某枚令牌的撤销。它不是宿主的三类业务事件，而是参与
// 偏序推理的独立节点：受保护操作必须严格发生在同令牌撤销之后。
type Revocation struct {
	ID    string
	Token TokenID
	After []string // 撤销发生在这些事件之后
}

// DeriveEdge 是一条受信任的派生关系边。
// Subject 与 Device 两类边共享该结构，由 Input 中的两个切片分别承载。
type DeriveEdge struct {
	From Instance
	To   Instance
}

// Input 是一次检测调用的完整输入。
type Input struct {
	Events         []Event
	Revocations    []Revocation
	SubjectDerives []DeriveEdge
	DeviceDerives  []DeriveEdge
}

// Verdict 是针对“是否存在完整攻击链”的三值结论。
type Verdict int

const (
	// VerdictNotEstablished 表示任何合法拓扑序下攻击链都不成立。
	VerdictNotEstablished Verdict = iota
	// VerdictPossible 表示存在至少一个合法拓扑序使攻击链成立，
	// 但并非所有拓扑序都成立（缺少至少一条因果边）。
	VerdictPossible
	// VerdictCertain 表示存在一条证据在每个合法拓扑序中都成立。
	VerdictCertain
)

// String 返回结论的可读名称。
func (v Verdict) String() string {
	switch v {
	case VerdictCertain:
		return "certain"
	case VerdictPossible:
		return "possible"
	default:
		return "not-established"
	}
}

// Evidence 是一条三段证据，引用读取、认证、操作三个事件以及判定
// “操作发生在撤销之后”所用的撤销节点。
type Evidence struct {
	ReadEventID      string
	AuthEventID      string
	OperationEventID string
	RevocationID     string
	// MissingEdges 列出尚不能由输入偏序保证的因果边，仅可能结论会非空；
	// 必然结论下该切片为空，这也是“缺少因果边不得报确定攻击”的落地检查点。
	MissingEdges []OrderedPair
}

// OrderedPair 是一条要求“From 先于 To”的因果边。
type OrderedPair struct {
	From string
	To   string
}

// Report 是检测结果。
type Report struct {
	Verdict Verdict
	// Certain 与 Possible 分别承载必然与可能的证据；不成立时两者均为空。
	Certain  []Evidence
	Possible []Evidence
}
