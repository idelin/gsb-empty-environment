package netsec

// 受限脚本语言允许的字符串常量。外部字符串（External）取值域同样限于这五个。
const (
	StrA           = "a"
	StrB           = "b"
	StrProto       = "__proto__"
	StrConstructor = "constructor"
	StrPrototype   = "prototype"
)

// AllowedStrings 是字符串常量与外部字符串的全部合法取值。
var AllowedStrings = []string{
	StrA,
	StrB,
	StrProto,
	StrConstructor,
	StrPrototype,
}

// 语言限制：抽象对象（分配点）最多 32 个，循环最多展开 16 次。
const (
	MaxObjects = 32
	MaxLoops   = 16
)

// Node 是所有 AST 节点的公共接口。ID 在整个 Program 内必须唯一且非空，
// 分析报告通过该 ID 定位可达的危险写节点。
type Node interface {
	NodeID() string
}

// Program 是受限脚本的根节点，Body 按顺序执行。
type Program struct {
	ID   string `json:"id"`
	Body []Node `json:"body"`
}

func (p *Program) NodeID() string { return p.ID }

// Alloc 分配一个新对象。对象默认原型为共享原型 SharedProtoID，
// 结果绑定到 Dest。每个分配点对应一个抽象对象。
type Alloc struct {
	ID   string `json:"id"`
	Dest string `json:"dest"`
}

func (n *Alloc) NodeID() string { return n.ID }

// Const 把字符串常量绑定到 Dest，Value 必须属于 AllowedStrings。
type Const struct {
	ID    string `json:"id"`
	Dest  string `json:"dest"`
	Value string `json:"value"`
}

func (n *Const) NodeID() string { return n.ID }

// External 把一个外部字符串绑定到 Dest。外部字符串可能是
// AllowedStrings 中的任意一个值，分析时按全集处理。
type External struct {
	ID   string `json:"id"`
	Dest string `json:"dest"`
}

func (n *External) NodeID() string { return n.ID }

// Alias 令 Dest 成为 Src 的别名（may/must 别名集合整体传播）。
type Alias struct {
	ID   string `json:"id"`
	Dest string `json:"dest"`
	Src  string `json:"src"`
}

func (n *Alias) NodeID() string { return n.ID }

// Load 读取 Obj.Key 字段并绑定到 Dest。
// 字段未定义时结果为空值（bottom），不产生错误。
type Load struct {
	ID   string `json:"id"`
	Dest string `json:"dest"`
	Obj  string `json:"obj"`
	Key  string `json:"key"`
}

func (n *Load) NodeID() string { return n.ID }

// Store 向 Obj.Key 写入 Val：
//   - Key 可能为 "__proto__" 时改变接收对象的原型；
//   - 接收对象可能为共享原型时，该节点是候选危险写。
type Store struct {
	ID  string `json:"id"`
	Obj string `json:"obj"`
	Key string `json:"key"`
	Val string `json:"val"`
}

func (n *Store) NodeID() string { return n.ID }

// If 是条件分支。分析不掌握谓词真值，两个分支都按可达处理并在结束时合并。
// Else 可为空（nil 或零长度）。
type If struct {
	ID   string `json:"id"`
	Then []Node `json:"then"`
	Else []Node `json:"else,omitempty"`
}

func (n *If) NodeID() string { return n.ID }

// Loop 按 Count 次有界展开其循环体，Count 必须在 [0, MaxLoops] 内。
// 分析按 0..Count 次执行的合并语义处理，保证不漏报。
type Loop struct {
	ID    string `json:"id"`
	Count int    `json:"count"`
	Body  []Node `json:"body"`
}

func (n *Loop) NodeID() string { return n.ID }

// Validate 做结构性校验：节点 ID 唯一非空、常量合法、分配点与循环次数受限。
// 通过校验的程序才能交给 Analyze。
func (p *Program) Validate() error {
	seen := map[string]bool{}
	allocs := 0
	var walk func(n Node) error
	checkID := func(id string) error {
		if id == "" {
			return &ValidationError{Msg: "node has empty id"}
		}
		if seen[id] {
			return &ValidationError{Msg: "duplicate node id: " + id}
		}
		seen[id] = true
		return nil
	}
	walkBlock := func(block []Node) error {
		for _, n := range block {
			if n == nil {
				return &ValidationError{Msg: "nil node in block"}
			}
			if err := walk(n); err != nil {
				return err
			}
		}
		return nil
	}
	walk = func(n Node) error {
		if err := checkID(n.NodeID()); err != nil {
			return err
		}
		switch t := n.(type) {
		case *Program:
			return walkBlock(t.Body)
		case *Alloc:
			allocs++
			if allocs > MaxObjects {
				return &ValidationError{Msg: "more than 32 object allocations"}
			}
			if t.Dest == "" {
				return &ValidationError{Msg: "alloc with empty dest at " + t.ID}
			}
		case *Const:
			if t.Dest == "" {
				return &ValidationError{Msg: "const with empty dest at " + t.ID}
			}
			if !isAllowedString(t.Value) {
				return &ValidationError{Msg: "illegal string constant at " + t.ID + ": " + t.Value}
			}
		case *External:
			if t.Dest == "" {
				return &ValidationError{Msg: "external with empty dest at " + t.ID}
			}
		case *Alias:
			if t.Dest == "" || t.Src == "" {
				return &ValidationError{Msg: "alias with empty operand at " + t.ID}
			}
		case *Load:
			if t.Dest == "" || t.Obj == "" || t.Key == "" {
				return &ValidationError{Msg: "load with empty operand at " + t.ID}
			}
		case *Store:
			if t.Obj == "" || t.Key == "" || t.Val == "" {
				return &ValidationError{Msg: "store with empty operand at " + t.ID}
			}
		case *If:
			if err := walkBlock(t.Then); err != nil {
				return err
			}
			return walkBlock(t.Else)
		case *Loop:
			if t.Count < 0 || t.Count > MaxLoops {
				return &ValidationError{Msg: "loop count out of range [0,16] at " + t.ID}
			}
			return walkBlock(t.Body)
		default:
			return &ValidationError{Msg: "unknown node type"}
		}
		return nil
	}
	return walk(p)
}

func isAllowedString(s string) bool {
	for _, allowed := range AllowedStrings {
		if s == allowed {
			return true
		}
	}
	return false
}

// ValidationError 描述结构性校验失败。
type ValidationError struct {
	Msg string
}

func (e *ValidationError) Error() string { return e.Msg }
