package netsec

import "fmt"

// NumSlots 是地址池的槽位数，固定为 3。
const NumSlots = 3

// MaxThreads 是脚本允许的最大线程数。
const MaxThreads = 3

// Ptr 是指向地址池中某个槽的指针值；Slot < 0 表示空指针。
// Gen 是该槽在指针被读取/分配时刻的代次，用于带代次 CAS 的比较。
type Ptr struct {
	Slot int
	Gen  int
}

// NilPtr 是空指针。
var NilPtr = Ptr{Slot: -1}

// IsNil 报告指针是否为空。
func (p Ptr) IsNil() bool { return p.Slot < 0 }

// OpKind 是脚本指令类别。
type OpKind int

const (
	OpRead    OpKind = iota // read dst        : dst = head
	OpNext                  // next dst src    : dst = deref(src).next（裸内存读，不看代次）
	OpAlloc                 // alloc dst #ev   : 从地址池分配槽，代次递增，写入事件 ev
	OpFree                  // free src        : 把 src 指向的槽归还地址池
	OpCAS                   // cas exp new     : CAS(head, exp, new)
	OpSetNext               // setnext n v     : deref(n).next = v
)

// Op 是一条脚本指令。Dst/Src/New 是线程局部寄存器名，Event 仅用于 OpAlloc。
type Op struct {
	Kind  OpKind
	Dst   string
	Src   string
	New   string
	Event int
}

// Read 构造读头指针指令。
func Read(dst string) Op { return Op{Kind: OpRead, Dst: dst} }

// Next 构造读 next 域指令。
func Next(dst, src string) Op { return Op{Kind: OpNext, Dst: dst, Src: src} }

// Alloc 构造分配指令，事件 ID 必须为正且全脚本唯一。
func Alloc(dst string, event int) Op { return Op{Kind: OpAlloc, Dst: dst, Event: event} }

// Free 构造释放指令。
func Free(src string) Op { return Op{Kind: OpFree, Src: src} }

// CAS 构造比较交换指令：期望 Src 寄存器的值，成功则把头指针换成 New 寄存器的值。
func CAS(exp, newVal string) Op { return Op{Kind: OpCAS, Src: exp, New: newVal} }

// SetNext 构造写 next 域指令。
func SetNext(node, val string) Op { return Op{Kind: OpSetNext, Src: node, New: val} }

func (o Op) String() string {
	switch o.Kind {
	case OpRead:
		return fmt.Sprintf("read %s", o.Dst)
	case OpNext:
		return fmt.Sprintf("next %s %s", o.Dst, o.Src)
	case OpAlloc:
		return fmt.Sprintf("alloc %s #%d", o.Dst, o.Event)
	case OpFree:
		return fmt.Sprintf("free %s", o.Src)
	case OpCAS:
		return fmt.Sprintf("cas %s %s", o.Src, o.New)
	case OpSetNext:
		return fmt.Sprintf("setnext %s %s", o.Src, o.New)
	}
	return "?"
}

// Semantics 选择 CAS 的比较方式。
type Semantics int

const (
	// RawAddr 裸地址 CAS：只比较槽地址，ABA 下会误判成功。
	RawAddr Semantics = iota
	// Tagged 带代次 CAS：同时比较槽地址与代次。
	Tagged
)

type slotMem struct {
	gen   int
	event int
	next  Ptr
}

// machine 是一种语义下的完整机器状态：共享队列、地址池与各线程寄存器。
type machine struct {
	head  Ptr
	slots [NumSlots]slotMem
	free  []int
	pc    []int
	regs  []map[string]Ptr
}

func newMachine(nThreads int) *machine {
	m := &machine{
		head: NilPtr,
		free: []int{0, 1, 2},
		pc:   make([]int, nThreads),
		regs: make([]map[string]Ptr, nThreads),
	}
	for i := range m.regs {
		m.regs[i] = map[string]Ptr{}
	}
	for s := range m.slots {
		m.slots[s] = slotMem{next: NilPtr}
	}
	return m
}

func (m *machine) clone() *machine {
	c := &machine{
		head:  m.head,
		slots: m.slots,
		free:  append([]int(nil), m.free...),
		pc:    append([]int(nil), m.pc...),
		regs:  make([]map[string]Ptr, len(m.regs)),
	}
	for i, r := range m.regs {
		cr := make(map[string]Ptr, len(r))
		for k, v := range r {
			cr[k] = v
		}
		c.regs[i] = cr
	}
	return c
}

func (m *machine) reg(t int, name string) Ptr {
	if name == "" {
		return NilPtr
	}
	if p, ok := m.regs[t][name]; ok {
		return p
	}
	return NilPtr
}

func (m *machine) setReg(t int, name string, p Ptr) {
	if name == "" || name == "_" {
		return
	}
	m.regs[t][name] = p
}

// apply 在机器上为线程 t 执行一条指令；若指令是 CAS，返回是否成功。
func (m *machine) apply(t int, op Op, sem Semantics) (casOK bool) {
	switch op.Kind {
	case OpRead:
		m.setReg(t, op.Dst, m.head)
	case OpNext:
		p := m.reg(t, op.Src)
		if p.IsNil() {
			m.setReg(t, op.Dst, NilPtr)
		} else {
			m.setReg(t, op.Dst, m.slots[p.Slot].next)
		}
	case OpAlloc:
		if len(m.free) == 0 {
			m.setReg(t, op.Dst, NilPtr)
			break
		}
		s := m.free[len(m.free)-1]
		m.free = m.free[:len(m.free)-1]
		m.slots[s].gen++
		m.slots[s].event = op.Event
		m.slots[s].next = NilPtr
		m.setReg(t, op.Dst, Ptr{Slot: s, Gen: m.slots[s].gen})
	case OpFree:
		if p := m.reg(t, op.Src); !p.IsNil() {
			m.free = append(m.free, p.Slot)
		}
	case OpSetNext:
		if p := m.reg(t, op.Src); !p.IsNil() {
			m.slots[p.Slot].next = m.reg(t, op.New)
		}
	case OpCAS:
		exp := m.reg(t, op.Src)
		nv := m.reg(t, op.New)
		var ok bool
		if sem == RawAddr {
			ok = m.head.Slot == exp.Slot
		} else {
			ok = m.head == exp
		}
		if ok {
			m.head = nv
		}
		return ok
	}
	return false
}

// reachableEvents 返回从头指针可达的事件 ID 集合（带环保护）。
func (m *machine) reachableEvents() map[int]bool {
	out := map[int]bool{}
	var visited [NumSlots]bool
	for p := m.head; !p.IsNil() && p.Slot < NumSlots && !visited[p.Slot]; {
		visited[p.Slot] = true
		if ev := m.slots[p.Slot].event; ev != 0 {
			out[ev] = true
		}
		p = m.slots[p.Slot].next
	}
	return out
}

// Validate 检查脚本合法性：线程数、指令字段、事件 ID 唯一且为正。
func Validate(scripts [][]Op) error {
	if len(scripts) == 0 {
		return fmt.Errorf("至少需要一个线程")
	}
	if len(scripts) > MaxThreads {
		return fmt.Errorf("线程数 %d 超过上限 %d", len(scripts), MaxThreads)
	}
	events := map[int]bool{}
	for t, sc := range scripts {
		for i, op := range sc {
			where := fmt.Sprintf("T%d 第 %d 条指令", t, i+1)
			switch op.Kind {
			case OpRead:
				if op.Dst == "" {
					return fmt.Errorf("%s: read 缺少目标寄存器", where)
				}
			case OpNext:
				if op.Dst == "" || op.Src == "" {
					return fmt.Errorf("%s: next 缺少寄存器参数", where)
				}
			case OpAlloc:
				if op.Dst == "" {
					return fmt.Errorf("%s: alloc 缺少目标寄存器", where)
				}
				if op.Event <= 0 {
					return fmt.Errorf("%s: alloc 事件 ID 必须为正整数, 得到 %d", where, op.Event)
				}
				if events[op.Event] {
					return fmt.Errorf("%s: 事件 ID %d 重复分配", where, op.Event)
				}
				events[op.Event] = true
			case OpFree:
				if op.Src == "" {
					return fmt.Errorf("%s: free 缺少源寄存器", where)
				}
			case OpCAS:
				if op.Src == "" || op.New == "" {
					return fmt.Errorf("%s: cas 缺少期望/新值寄存器", where)
				}
			case OpSetNext:
				if op.Src == "" || op.New == "" {
					return fmt.Errorf("%s: setnext 缺少节点/值寄存器", where)
				}
			default:
				return fmt.Errorf("%s: 未知指令类别 %d", where, op.Kind)
			}
		}
	}
	return nil
}
