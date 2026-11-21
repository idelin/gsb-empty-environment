// Package handover 规划并验证宿主向低权限子进程交接句柄能力的过程。
//
// 核心抽象：
//   - 句柄表中的每个槽位带不可回绕的代次（generation），槽位复用时代次单调递增；
//   - 角色声明允许的对象身份、所需权限与共享关系；
//   - 规划结果是一组可逐步模拟的操作（交换、经唯一临时槽的旋转、cloexec 调整等）。
package handover

import "fmt"

// Perm 是打开对象上的能力位集合。
type Perm uint8

const (
	PermRead  Perm = 1 << iota // 可读
	PermWrite                  // 可写
)

// SubsetOf 报告 p 是否为 q 的子集。
func (p Perm) SubsetOf(q Perm) bool { return p&^q == 0 }

func (p Perm) Readable() bool { return p&PermRead != 0 }
func (p Perm) Writable() bool { return p&PermWrite != 0 }

func (p Perm) String() string {
	switch p {
	case 0:
		return "none"
	case PermRead:
		return "r"
	case PermWrite:
		return "w"
	case PermRead | PermWrite:
		return "rw"
	default:
		return fmt.Sprintf("perm(%d)", uint8(p))
	}
}

// ObjectID 是打开对象的身份标识。两个槽位具有相同 ObjectID 即互为别名。
type ObjectID string

// Handle 是抽象句柄表中的一项。
type Handle struct {
	Slot    int      // 槽号
	Gen     uint64   // 不可回绕的槽代次
	Object  ObjectID // 打开对象身份
	Perm    Perm     // 实际持有的读写权限
	Cloexec bool     // true 表示执行时自动关闭、子进程不继承
}

// Table 是某一时刻的抽象句柄表快照。
type Table struct {
	Handles []Handle
}

// BySlot 返回以槽号索引的句柄映射。
func (t *Table) BySlot() map[int]Handle {
	m := make(map[int]Handle, len(t.Handles))
	for _, h := range t.Handles {
		m[h.Slot] = h
	}
	return m
}

// Role 是子进程声明的一个能力角色。
type Role struct {
	Name    string
	Target  int // 子进程中固定的目标槽号
	Allowed map[ObjectID]struct{}
	Need    Perm                // 所需权限；交接句柄不得含任何额外权限
	Share   map[string]struct{} // 允许与之共享同一打开对象的角色名集合
}

// Allow 构造允许对象身份集合的辅助函数。
func Allow(ids ...ObjectID) map[ObjectID]struct{} {
	m := make(map[ObjectID]struct{}, len(ids))
	for _, id := range ids {
		m[id] = struct{}{}
	}
	return m
}

// ShareWith 构造可共享角色集合的辅助函数。
func ShareWith(names ...string) map[string]struct{} {
	m := make(map[string]struct{}, len(names))
	for _, n := range names {
		m[n] = struct{}{}
	}
	return m
}

func (r *Role) allowsObject(id ObjectID) bool {
	_, ok := r.Allowed[id]
	return ok
}

func (r *Role) allowsShare(other string) bool {
	_, ok := r.Share[other]
	return ok
}

// Assignment 是单个角色的分配结果。
type Assignment struct {
	RoleName string
	Slot     int
	Object   ObjectID
	Perm     Perm
	Target   int
}

// OpKind 标识规划操作的种类。
type OpKind uint8

const (
	OpSwap       OpKind = iota // 交换两个已占用槽
	OpParSwap                  // 原子并发交换若干互不相交的槽对
	OpLoad                     // 源槽内容加载到临时槽（源槽变空，临时槽此前必须为空）
	OpMove                     // 已占用源槽移动到空目标槽
	OpStore                    // 临时槽内容存入空目标槽（临时槽变空）
	OpDropTemp                 // 释放临时槽，要求其已为空
	OpBarrier                  // 权限边界：临时槽必须为空
	OpSetCloexec               // 设置某槽的执行时自动关闭标志
	OpClose                    // 关闭句柄并清空槽位
)

// Op 是交接计划中的一步操作。
type Op struct {
	Kind  OpKind
	A, B  int
	On    bool     // OpSetCloexec 使用
	Pairs [][2]int // OpParSwap 使用
	Bound Perm     // OpBarrier 标注的边界权限
}

func (k OpKind) String() string {
	switch k {
	case OpSwap:
		return "swap"
	case OpParSwap:
		return "parswap"
	case OpLoad:
		return "load"
	case OpMove:
		return "move"
	case OpStore:
		return "store"
	case OpDropTemp:
		return "droptemp"
	case OpBarrier:
		return "barrier"
	case OpSetCloexec:
		return "setcloexec"
	case OpClose:
		return "close"
	default:
		return fmt.Sprintf("op(%d)", uint8(k))
	}
}

func (o Op) String() string {
	switch o.Kind {
	case OpSwap:
		return fmt.Sprintf("swap(%d,%d)", o.A, o.B)
	case OpParSwap:
		return fmt.Sprintf("parswap%v", o.Pairs)
	case OpLoad:
		return fmt.Sprintf("load(temp=%d,%d)", o.A, o.B)
	case OpMove:
		return fmt.Sprintf("move(%d->%d)", o.A, o.B)
	case OpStore:
		return fmt.Sprintf("store(temp=%d->%d)", o.A, o.B)
	case OpDropTemp:
		return fmt.Sprintf("droptemp(%d)", o.A)
	case OpBarrier:
		return fmt.Sprintf("barrier(%s)", o.Bound)
	case OpSetCloexec:
		return fmt.Sprintf("setcloexec(%d,on=%v)", o.A, o.On)
	case OpClose:
		return fmt.Sprintf("close(%d)", o.A)
	default:
		return o.Kind.String()
	}
}

// SlotExpect 是交接前某相关槽必须满足的状态。
type SlotExpect struct {
	Gen    uint64
	Object ObjectID
	Empty  bool // true 表示提交时该槽必须为空
}

// Plan 是完整的交接规划。
type Plan struct {
	TempSlot    int // 宿主提供的唯一临时槽
	Assignments []Assignment
	Ops         []Op

	// Expect 是交接前需要原子核对的相关槽及其期望的代次与对象身份。
	Expect map[int]SlotExpect

	UsedSlots   []int      // 选中的源槽（角色顺序）
	UsedObjects []ObjectID // 去重后的源对象
}

// LiveView 是宿主实时句柄表的只读视图。
type LiveView interface {
	HandleAt(slot int) (Handle, bool)
}
