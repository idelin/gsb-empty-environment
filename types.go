package netsec

// NodeKind 标识控制流图节点的语义类别。
type NodeKind string

const (
	// KindAuthCheck 授权检查节点，对 Node.Subject 访问 Node.Object 执行校验，
	// 按对象当前版本产生一条凭据。
	KindAuthCheck NodeKind = "auth_check"

	// KindModifyVersion 对象版本修改节点，提升 Node.Object 的版本，
	// 该对象此前的旧版本凭据保留在状态中，但与新版本不再匹配。
	KindModifyVersion NodeKind = "modify_version"

	// KindTransferCredential 授权凭据转移节点，仅原样传递已有凭据，
	// 不改变凭据绑定的主体、对象与版本。
	KindTransferCredential NodeKind = "transfer_credential"

	// KindSensitiveCall 敏感调用节点，调用前必须持有主体、对象、版本
	// 三者完全匹配的凭据，否则计入报警。
	KindSensitiveCall NodeKind = "sensitive_call"

	// KindPlain 无安全语义的普通节点，状态原样传递。
	KindPlain NodeKind = "plain"
)

// EdgeKind 标识控制流边的种类。
type EdgeKind string

const (
	// EdgeNormal 普通顺序控制流边。
	EdgeNormal EdgeKind = "normal"

	// EdgeException 异常边，代表异常/错误处理出口。异常路径与正常路径
	// 一样参与合流与路径枚举，不能跳过授权保证。
	EdgeException EdgeKind = "exception"
)

// Credential 是授权检查留下的（主体, 对象, 版本）三元组。
// 凭据只能证明原主体 Subject 对原对象 Object 的原版本 Version 拥有授权，
// 不随对象版本修改自动升级，也不随凭据转移扩大主体范围。
type Credential struct {
	Subject string
	Object  string
	Version string
}

// Node 是控制流图上的一个节点。
//
// Subject 与 Object 的含义随 NodeKind 而定：
//   - KindAuthCheck：Subject 为被校验主体，Object 为被校验对象；
//   - KindModifyVersion：Object 为被修改对象，Subject 忽略；
//   - KindSensitiveCall：Subject 为发起调用的主体，Object 为目标对象；
//   - 其余类型忽略这两个字段。
type Node struct {
	ID      string
	Kind    NodeKind
	Subject string
	Object  string
}

// Edge 是控制流图上的一条有向边。Kind 为空时按 [EdgeNormal] 处理。
type Edge struct {
	From string
	To   string
	Kind EdgeKind
}

// kind 归一化边类型，空值视为普通边。
func (e Edge) kind() EdgeKind {
	if e.Kind == "" {
		return EdgeNormal
	}
	return e.Kind
}

// Graph 是待分析的控制流图。Entry 是唯一入口节点 ID。
type Graph struct {
	Entry string
	Nodes []Node
	Edges []Edge
}

// PathStep 描述最短违反路径上的一步。
type PathStep struct {
	NodeID string

	// Via 是进入本节点所经过边的类型；入口节点为 [EdgeNormal]。
	Via EdgeKind
}

// Finding 描述一个缺少授权保证的敏感调用。
type Finding struct {
	// NodeID 是缺失保证的敏感调用节点 ID。
	NodeID string

	// Subject 与 Object 取自该敏感调用节点。
	Subject string
	Object  string

	// RequiredVersion 是调用点对象的当前版本；当不同到达路径对版本
	// 无共同认识时为 [UnknownVersion]。
	RequiredVersion string

	// Reason 是机器可读的缺失原因说明。
	Reason string

	// Path 是从入口到该调用的一条边数最少、且缺少匹配凭据的路径。
	Path []PathStep
}

// Result 是 [Analyze] 的分析结果。
type Result struct {
	// Findings 按节点在 Graph.Nodes 中的出现顺序排列。
	Findings []Finding

	// Iterations 是不动点迭代轮数（每轮按节点顺序做一次全部传播）。
	Iterations int

	// Unreachable 是入口不可达的节点 ID。
	Unreachable []string

	// states 与 nodes 保留不动点状态，供 StateAt 查询。
	states map[string]flowState
	nodes  map[string]Node
}

// StateView 是 [Analyze].StateAt 返回的宿主可读抽象状态视图。
type StateView struct {
	// Versions 是各对象在所有到达路径上共同的当前版本；
	// 值为 [UnknownVersion] 表示版本在路径间不一致。
	Versions map[string]string

	// Credentials 是所有到达路径共同持有的凭据集合。
	Credentials []Credential

	// Reachable 报告节点是否从入口可达。
	Reachable bool
}

const (
	// InitialVersion 是对象在入口处的初始版本标识。
	InitialVersion = "v0"

	// ModifiedVersion 是版本修改节点产生的新版本标识前缀，
	// 实际值形如 "v:<nodeID>"，保证不同修改点互不相同。
	ModifiedVersion = "v:"

	// UnknownVersion 表示合流后各路径对对象版本没有共同认识。
	// 任何具体版本的凭据都不能匹配未知版本。
	UnknownVersion = "*unknown*"
)
