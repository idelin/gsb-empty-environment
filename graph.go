package netsec

// NodeKind 标识输入图中节点的类型。
type NodeKind int

const (
	// KindString 字符串叶子节点，值存于 Node.Str。
	KindString NodeKind = iota
	// KindArray 数组节点，子节点为 Node.Items 中的节点下标。
	KindArray
	// KindDict 字典节点，子节点为 Node.Fields 中的节点下标。
	KindDict
	// KindRef 对象引用节点，指向 Node.Ref 下标的节点；
	// 解码时是透明别名，与目标共享同一 *Object 身份。
	KindRef
	// KindCap 能力引用节点，仅携带不可猜测的 Node.Token，
	// 由可信注册器解析为租户、操作集合、代次与撤销状态。
	KindCap
)

// Node 是攻击者可控的输入图节点。子节点一律以下标引用，
// 因此共享与环都可以直接表达。
type Node struct {
	Kind   NodeKind
	Str    string         // KindString
	Items  []int          // KindArray：子节点下标
	Fields map[string]int // KindDict：键 -> 子节点下标
	Ref    int            // KindRef：目标节点下标
	Token  string         // KindCap：能力 token
}

// Graph 是待解码的输入图，根节点固定为 Nodes[0]。
// 整图（含不可达节点）都会被验证，图中出现的全部能力都会被租约覆盖。
type Graph struct {
	Nodes []Node
}

// Object 是解码后的对象图节点。共享子图与环通过指针身份保持：
// 输入中同一节点下标在输出中恒为同一 *Object。
type Object struct {
	Kind   NodeKind
	Str    string
	Items  []*Object
	Fields map[string]*Object
	Cap    *Capability
}

// Capability 是解码成功后交给插件的能力句柄。
// 它只暴露注册器解析出的元数据，本身不可被执行；
// 宿主在调用插件前用 Allows 做操作级判定。
type Capability struct {
	tenant     string
	ops        map[string]bool
	generation int
}

// Tenant 返回能力所属租户。
func (c *Capability) Tenant() string { return c.tenant }

// Generation 返回能力 token 的代次。
func (c *Capability) Generation() int { return c.generation }

// Allows 报告能力是否包含指定操作。
func (c *Capability) Allows(op string) bool { return c.ops[op] }
