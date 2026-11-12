package netsec

// BlockID 是基本块的源节点 ID。
type BlockID int

// InstrID 是指令的源节点 ID（插桩前后均保持稳定、唯一）。
type InstrID int

// Reg 是 SSA 寄存器（值编号）。0 保留为“无值”。
type Reg int

// ValueID 统一引用参数寄存器或指令结果寄存器。
type ValueID = Reg

// Kind 区分内存访问类型，供检测回调判断。
type Kind int

const (
	KindRead Kind = iota + 1
	KindWrite
)

func (k Kind) String() string {
	switch k {
	case KindRead:
		return "read"
	case KindWrite:
		return "write"
	default:
		return "unknown"
	}
}

// Access 描述一次待检测的内存访问，地址与长度来自原指令的操作数。
type Access struct {
	Kind   Kind
	Addr   int64
	Size   int
	Source InstrID  // 触发检测的原 load/store 节点 ID
	Pos    Position // 原源位置
}

// Checker 是宿主提供的纯检测回调。
//
// 实现必须是纯函数：不得读写被保护内存、不得有可观察副作用，
// 仅根据 (Addr,Size,Kind) 决定访问是否合法。返回 error 表示拒绝。
type Checker interface {
	Check(Access) error
}

// CheckerFunc 让普通函数满足 Checker。
type CheckerFunc func(Access) error

// Check 实现 Checker。
func (fn CheckerFunc) Check(a Access) error { return fn(a) }
