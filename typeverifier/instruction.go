package typeverifier

// Opcode 标识一条已解码指令的操作码。
type Opcode int

const (
	// OpNew 分配对象：new(类)，产生带该指令 ID 的未初始化引用。
	OpNew Opcode = iota + 1
	// OpDup 复制操作数栈栈顶。
	OpDup
	// OpAStore 弹栈并写入局部槽：astore(槽)。
	OpAStore
	// OpALoad 把局部槽压栈：aload(槽)。
	OpALoad
	// OpInit 从栈顶取接收者调用构造器：init(类)。
	OpInit
	// OpUse 使用一个引用：use(类)。
	OpUse
	// OpGoto 无条件分支。
	OpGoto
	// OpBranch 非确定双分支，两个后继都可能到达。
	OpBranch
	// OpReturn 正常返回，要求操作数栈为空。
	OpReturn
	// OpThrow 抛出栈顶引用，要求是已初始化的 Throwable。
	OpThrow
)

// Instruction 是已解码指令数组中的一条指令，PC 即其下标。
// Class 用于 new/init/use；Slot 用于 astore/aload；
// TargetA 用于 goto，TargetA/TargetB 用于非确定双分支。
type Instruction struct {
	Op      Opcode
	Class   string
	Slot    int
	TargetA int
	TargetB int
}

// Handler 是异常表的一项：覆盖 [Start, End) 指令区间，
// 捕获 CatchClass（空串按 Throwable 处理）并跳转到 HandlerPC。
type Handler struct {
	Start      int
	End        int
	HandlerPC  int
	CatchClass string
}

// DefaultThrowable 是异常处理器压入操作数栈的已初始化异常根类型。
const DefaultThrowable = "Throwable"

// Method 是交给校验器的一个方法视图：指令数组、异常表、局部槽数量与最大栈深。
type Method struct {
	Code      []Instruction
	Handlers  []Handler
	MaxLocals int
	MaxStack  int
	// Throwable 是宿主异常体系的根类名，必须是层次树中的已初始化类；
	// 留空时使用 DefaultThrowable。
	Throwable string
}
