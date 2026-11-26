// Package subverify 实现上下文敏感的 jsr/ret 清理子程序字节码验证。
//
// 验证按“调用点链”区分分析上下文：每个 jsr 调用点产生独立上下文，
// 不同调用方的返回令牌永远不会在合流处被并成任意跳转目标。返回令牌
// 与整数互不兼容；ret 只接受当前最内层调用的令牌，返回后该令牌在所
// 有局部槽和操作数栈中的别名全部失效（变为不可用值）。
package subverify

import (
	"fmt"
	"strconv"
	"strings"
)

// 支持的操作码。
const (
	JSR Op = iota
	RET
	StoreReturn
	LoadReturn
	PushInt
	StoreInt
	LoadInt
	Pop
	Goto
	Branch // 非确定分支：下一状态可能是 Target 或顺序下一条指令
	Return
)

// Op 是指令操作码。
type Op uint8

// Instr 描述一条指令。Target 供 JSR/Goto/Branch 使用，
// Slot 供 RET/StoreReturn/LoadReturn/StoreInt/LoadInt 使用，
// Value 供 PushInt 使用。
type Instr struct {
	Op     Op
	Target int
	Slot   int
	Value  int
}

// 验证结论。
const (
	// Rejected 表示程序确定不可加载。
	Rejected Status = iota
	// Loadable 表示程序通过全部检查，可以加载。
	Loadable
	// Unknown 表示分析预算耗尽，既不能确认可加载也不能确认拒绝。
	Unknown
)

// Status 是验证结论。
type Status uint8

func (s Status) String() string {
	switch s {
	case Rejected:
		return "Rejected"
	case Loadable:
		return "Loadable"
	case Unknown:
		return "Unknown"
	default:
		return "Status(" + strconv.Itoa(int(s)) + ")"
	}
}

// CallSite 记录调用点链上的一环：哪条 jsr、进入哪个子程序、返回地址是多少。
type CallSite struct {
	JSRIndex   int
	Target     int
	ReturnAddr int
}

func (c CallSite) String() string {
	return fmt.Sprintf("jsr@%d -> %d (ret@%d)", c.JSRIndex, c.Target, c.ReturnAddr)
}

// Result 是一次验证的结果。拒绝时 CallChain 给出拒绝发生前的调用点链
// （根上下文中的结构性拒绝链为空）。
type Result struct {
	Status    Status
	Reason    string
	CallChain []CallSite
}

func (r *Result) String() string {
	var b strings.Builder
	b.WriteString(r.Status.String())
	if r.Reason != "" {
		b.WriteString(": ")
		b.WriteString(r.Reason)
	}
	if len(r.CallChain) > 0 {
		b.WriteString(" [调用点链: ")
		for i, c := range r.CallChain {
			if i > 0 {
				b.WriteString(" -> ")
			}
			b.WriteString(c.String())
		}
		b.WriteString("]")
	}
	return b.String()
}

// Config 控制验证过程。MaxBudget<=0 时使用默认预算。
type Config struct {
	MaxBudget int
}

// 槽数与栈深的硬上限。
const (
	MaxSlots = 32
	MaxStack = 32
	// MaxCallDepth 是子程序嵌套层数上限（根上下文为第 0 层）。
	MaxCallDepth  = 4
	defaultBudget = 200000
)

type valTag uint8

const (
	topVal valTag = iota // 不可用：未初始化或合流不相容
	intVal
	retVal
)

// absVal 是操作数栈/局部槽中的抽象值。retVal 时 tok 非空。
type absVal struct {
	tag valTag
	tok *retToken
}

// retToken 是某次 jsr 调用独有的返回令牌。
type retToken struct {
	call    *callNode
	retAddr int
}

// callNode 是调用点链上的一个上下文节点；nil 表示根上下文。
type callNode struct {
	parent     *callNode
	jsr        int
	target     int
	retAddr    int
	depth      int
	entryDepth int
	token      *retToken
}

type ctxKey struct {
	parent *callNode
	jsr    int
}

type stateKey struct {
	ctx *callNode
	pc  int
}

type workItem struct {
	ctx *callNode
	pc  int
}

type halt struct {
	res *Result
}

type verifier struct {
	prog      []Instr
	slotCount int
	budget    int
	nodes     map[ctxKey]*callNode
	states    map[stateKey]frame
	queue     []workItem
	head      int
}

// Verify 对程序做上下文敏感验证。slotCount 为局部槽数量。
func Verify(prog []Instr, slotCount int, cfg Config) *Result {
	v := &verifier{
		prog:      prog,
		slotCount: slotCount,
		budget:    cfg.MaxBudget,
		nodes:     map[ctxKey]*callNode{},
		states:    map[stateKey]frame{},
	}
	if v.budget <= 0 {
		v.budget = defaultBudget
	}
	return v.run()
}

func (v *verifier) run() (res *Result) {
	defer func() {
		if h := recover(); h != nil {
			if stop, ok := h.(halt); ok {
				res = stop.res
				return
			}
			panic(h)
		}
	}()

	v.structuralCheck()

	initial := frame{locals: make([]absVal, v.slotCount)}
	v.edge(nil, 0, initial)

	for v.head < len(v.queue) {
		if v.budget <= 0 {
			v.stop(Unknown, nil, nil, "分析预算耗尽，无法给出可加载结论")
		}
		item := v.queue[v.head]
		v.head++
		v.budget--
		v.process(item.ctx, item.pc, v.states[stateKey{item.ctx, item.pc}])
	}
	return &Result{Status: Loadable}
}

func (v *verifier) structuralCheck() {
	if len(v.prog) == 0 {
		v.stop(Rejected, nil, nil, "指令数组为空")
	}
	if v.slotCount < 0 || v.slotCount > MaxSlots {
		v.stop(Rejected, nil, nil, fmt.Sprintf("局部槽数量 %d 超出 [0,%d]", v.slotCount, MaxSlots))
	}
	for i, ins := range v.prog {
		switch ins.Op {
		case JSR:
			v.checkTarget(i, ins.Target)
			if i+1 >= len(v.prog) {
				v.stop(Rejected, nil, nil, fmt.Sprintf("pc %d 的 jsr 没有顺序返回位置", i))
			}
		case Goto, Branch:
			v.checkTarget(i, ins.Target)
			if ins.Op == Branch && i+1 >= len(v.prog) {
				v.stop(Rejected, nil, nil, fmt.Sprintf("pc %d 的 branch 没有顺序后继", i))
			}
		case RET, StoreReturn, LoadReturn, StoreInt, LoadInt:
			if ins.Slot < 0 || ins.Slot >= v.slotCount {
				v.stop(Rejected, nil, nil, fmt.Sprintf("pc %d 使用非法槽 %d", i, ins.Slot))
			}
		case PushInt, Pop, Return:
		default:
			v.stop(Rejected, nil, nil, fmt.Sprintf("pc %d 含未知操作码 %d", i, ins.Op))
		}
	}
}

func (v *verifier) checkTarget(i, target int) {
	if target < 0 || target >= len(v.prog) {
		v.stop(Rejected, nil, nil, fmt.Sprintf("pc %d 跳转到越界目标 %d", i, target))
	}
}

type frame struct {
	locals []absVal
	stack  []absVal
}

func cloneFrame(f frame) frame {
	nf := frame{
		locals: append([]absVal(nil), f.locals...),
		stack:  append([]absVal(nil), f.stack...),
	}
	return nf
}

func valName(v absVal) string {
	switch v.tag {
	case intVal:
		return "int"
	case retVal:
		return "return-token"
	default:
		return "top/不可用"
	}
}

func (v *verifier) push(f frame, val absVal, pc int, ctx *callNode) frame {
	if len(f.stack) >= MaxStack {
		v.stop(Rejected, ctx, nil, fmt.Sprintf("pc %d 操作数栈深度超过 %d", pc, MaxStack))
	}
	f.stack = append(f.stack, val)
	return f
}

func (v *verifier) peek(f frame, pc int, ctx *callNode) absVal {
	if len(f.stack) == 0 {
		v.stop(Rejected, ctx, nil, fmt.Sprintf("pc %d 从空操作数栈取值", pc))
	}
	return f.stack[len(f.stack)-1]
}

func (v *verifier) process(ctx *callNode, pc int, f frame) {
	ins := v.prog[pc]
	switch ins.Op {
	case PushInt:
		nf := cloneFrame(f)
		nf = v.push(nf, absVal{tag: intVal}, pc, ctx)
		v.edge(ctx, pc+1, nf)

	case Pop:
		nf := cloneFrame(f)
		if len(nf.stack) == 0 {
			v.stop(Rejected, ctx, nil, fmt.Sprintf("pc %d 对空操作数栈执行 pop", pc))
		}
		nf.stack = nf.stack[:len(nf.stack)-1]
		v.edge(ctx, pc+1, nf)

	case StoreInt:
		nf := cloneFrame(f)
		top := v.peek(nf, pc, ctx)
		if top.tag != intVal {
			v.stop(Rejected, ctx, nil, fmt.Sprintf("pc %d storeInt 要求整数，栈顶为 %s", pc, valName(top)))
		}
		nf.stack = nf.stack[:len(nf.stack)-1]
		nf.locals[ins.Slot] = top
		v.edge(ctx, pc+1, nf)

	case LoadInt:
		nf := cloneFrame(f)
		slot := nf.locals[ins.Slot]
		if slot.tag != intVal {
			v.stop(Rejected, ctx, nil, fmt.Sprintf("pc %d loadInt 槽 %d 不含整数（%s）", pc, ins.Slot, valName(slot)))
		}
		nf = v.push(nf, slot, pc, ctx)
		v.edge(ctx, pc+1, nf)

	case StoreReturn:
		nf := cloneFrame(f)
		top := v.peek(nf, pc, ctx)
		if top.tag != retVal {
			v.stop(Rejected, ctx, nil, fmt.Sprintf("pc %d storeReturn 要求返回令牌，栈顶为 %s", pc, valName(top)))
		}
		nf.stack = nf.stack[:len(nf.stack)-1]
		nf.locals[ins.Slot] = top
		v.edge(ctx, pc+1, nf)

	case LoadReturn:
		nf := cloneFrame(f)
		slot := nf.locals[ins.Slot]
		if slot.tag != retVal {
			v.stop(Rejected, ctx, nil, fmt.Sprintf("pc %d loadReturn 槽 %d 不含有效返回令牌（%s）", pc, ins.Slot, valName(slot)))
		}
		nf = v.push(nf, slot, pc, ctx)
		v.edge(ctx, pc+1, nf)

	case Goto:
		v.edge(ctx, ins.Target, cloneFrame(f))

	case Branch:
		v.edge(ctx, pc+1, cloneFrame(f))
		if ins.Target != pc+1 {
			v.edge(ctx, ins.Target, cloneFrame(f))
		}

	case Return:
		if ctx != nil {
			v.stop(Rejected, ctx, nil, fmt.Sprintf("pc %d 方法 return 时活动调用栈非空（深度 %d）", pc, ctx.depth))
		}
		if len(f.stack) != 0 {
			v.stop(Rejected, nil, nil, fmt.Sprintf("pc %d 方法 return 时操作数栈非空（深度 %d）", pc, len(f.stack)))
		}

	case JSR:
		v.doJSR(ctx, pc, ins, f)

	case RET:
		v.doRET(ctx, pc, ins, f)
	}
}

func (v *verifier) doJSR(ctx *callNode, pc int, ins Instr, f frame) {
	if ctx != nil && ctx.depth >= MaxCallDepth {
		v.stop(Rejected, ctx, nil, fmt.Sprintf("pc %d 调用嵌套超过 %d 层", pc, MaxCallDepth))
	}
	for p := ctx; p != nil; p = p.parent {
		if p.target == ins.Target {
			v.stop(Rejected, ctx, nil, fmt.Sprintf("pc %d 递归进入活动子程序 %d", pc, ins.Target))
		}
	}
	child := v.internChild(ctx, pc, ins.Target)
	nf := cloneFrame(f)
	nf = v.push(nf, absVal{tag: retVal, tok: child.token}, pc, child)
	v.edge(child, ins.Target, nf)
}

func (v *verifier) doRET(ctx *callNode, pc int, ins Instr, f frame) {
	slot := f.locals[ins.Slot]
	if slot.tag != retVal {
		v.stop(Rejected, ctx, nil, fmt.Sprintf("pc %d ret 槽 %d 不含有效返回令牌（%s）", pc, ins.Slot, valName(slot)))
	}
	tok := slot.tok
	if ctx == nil || tok.call != ctx {
		v.stop(Rejected, ctx, nil, fmt.Sprintf("pc %d ret 使用的令牌不属于当前最内层调用", pc))
	}
	nf := cloneFrame(f)
	if len(nf.stack) != ctx.entryDepth {
		v.stop(Rejected, ctx, nil, fmt.Sprintf("pc %d ret 时操作数栈不平衡：深度 %d，期望 %d", pc, len(nf.stack), ctx.entryDepth))
	}
	invalidate(nf, tok)
	nf.stack = nf.stack[:ctx.entryDepth-1]
	v.edge(ctx.parent, tok.retAddr, nf)
}

func (v *verifier) internChild(parent *callNode, jsr, target int) *callNode {
	key := ctxKey{parent: parent, jsr: jsr}
	if node, ok := v.nodes[key]; ok {
		return node
	}
	depth := 1
	if parent != nil {
		depth = parent.depth + 1
	}
	entryDepth := 1
	if parent != nil {
		entryDepth = parent.entryDepth + 1
	}
	node := &callNode{parent: parent, jsr: jsr, target: target, retAddr: jsr + 1, depth: depth, entryDepth: entryDepth}
	node.token = &retToken{call: node, retAddr: jsr + 1}
	v.nodes[key] = node
	return node
}

// invalidate 把令牌 tok 的全部别名（局部槽与操作数栈）置为不可用，
// 包括子程序内部派生的任意拷贝。
func invalidate(f frame, tok *retToken) {
	for i, val := range f.locals {
		if val.tag == retVal && val.tok == tok {
			f.locals[i] = absVal{}
		}
	}
	for i, val := range f.stack {
		if val.tag == retVal && val.tok == tok {
			f.stack[i] = absVal{}
		}
	}
}

// edge 把 incoming 状态合入 (ctx,pc)。局部槽不相容退化为不可用，
// 操作数栈不相容直接拒绝。
func (v *verifier) edge(ctx *callNode, pc int, incoming frame) {
	key := stateKey{ctx: ctx, pc: pc}
	old, ok := v.states[key]
	if !ok {
		v.states[key] = incoming
		v.queue = append(v.queue, workItem{ctx: ctx, pc: pc})
		return
	}
	merged, changed := v.mergeFrames(ctx, old, incoming)
	if !changed {
		return
	}
	v.states[key] = merged
	v.queue = append(v.queue, workItem{ctx: ctx, pc: pc})
}

func (v *verifier) mergeFrames(ctx *callNode, a, b frame) (frame, bool) {
	changed := false
	merged := frame{locals: make([]absVal, len(a.locals)), stack: a.stack}
	for i := range a.locals {
		mv, ch := mergeVal(a.locals[i], b.locals[i])
		merged.locals[i] = mv
		changed = changed || ch
	}
	if len(a.stack) != len(b.stack) {
		v.stop(Rejected, ctx, nil, fmt.Sprintf("合流时操作数栈深度不相容：%d 与 %d", len(a.stack), len(b.stack)))
	}
	for i := range a.stack {
		mv, ch := mergeVal(a.stack[i], b.stack[i])
		if ch {
			merged.stack[i] = mv
			changed = true
		}
	}
	return merged, changed
}

// mergeVal 实现抽象值格：top 吸收一切；相同具体值保持；
// 不同整数并为整数（整数无类型差异）；其余不相容并为 top。
// 不同调用方的令牌并为 top，绝不并成可跳转的具体目标。
func mergeVal(a, b absVal) (absVal, bool) {
	if a.tag == topVal {
		return a, false
	}
	if b.tag == topVal {
		return b, true
	}
	if a.tag == intVal && b.tag == intVal {
		return a, false
	}
	if a.tag == retVal && b.tag == retVal && a.tok == b.tok {
		return a, false
	}
	return absVal{}, true
}

func (v *verifier) stop(status Status, ctx *callNode, extra *CallSite, reason string) {
	res := &Result{Status: status, Reason: reason}
	for p := ctx; p != nil; p = p.parent {
		res.CallChain = append([]CallSite{{
			JSRIndex:   p.jsr,
			Target:     p.target,
			ReturnAddr: p.retAddr,
		}}, res.CallChain...)
	}
	if extra != nil {
		res.CallChain = append(res.CallChain, *extra)
	}
	panic(halt{res: res})
}
