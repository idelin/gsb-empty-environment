// Package netsec 实现旧式字节码共享子程序（jsr/ret）的上下文敏感验证器。
//
// 验证目标：返回令牌只能由创建它的那次调用使用，ret 之后其所有别名失效，
// 恶意代码无法把别处的返回位置搬到 ret 里劫持控制流。
package netsec

import (
	"fmt"
	"strings"
)

// Op 是字节码操作码。
type Op int

const (
	OpJsr         Op = iota // jsr(target)：压入本次调用专属的返回令牌并进入子程序
	OpRet                   // ret(slot)：按局部槽中的返回令牌返回调用点
	OpStoreReturn           // storeReturn(slot)：弹出返回令牌存入局部槽
	OpLoadReturn            // loadReturn(slot)：把局部槽中的返回令牌压栈
	OpPushInt               // pushInt(v)：压入整数常量
	OpStoreInt              // storeInt(slot)：弹出整数存入局部槽
	OpLoadInt               // loadInt(slot)：把局部槽中的整数压栈
	OpPop                   // pop：弹出栈顶
	OpGoto                  // goto(target)
	OpBranch                // 非确定分支：两个目标都可能到达
	OpReturn                // 方法返回：要求调用栈与操作数栈均为空
)

// Instr 是一条指令。Arg 为目标地址 / 槽号 / 整数常量，Arg2 为分支第二目标。
type Instr struct {
	Op   Op
	Arg  int
	Arg2 int
}

func Jsr(target int) Instr       { return Instr{Op: OpJsr, Arg: target} }
func Ret(slot int) Instr         { return Instr{Op: OpRet, Arg: slot} }
func StoreReturn(slot int) Instr { return Instr{Op: OpStoreReturn, Arg: slot} }
func LoadReturn(slot int) Instr  { return Instr{Op: OpLoadReturn, Arg: slot} }
func PushInt(v int) Instr        { return Instr{Op: OpPushInt, Arg: v} }
func StoreInt(slot int) Instr    { return Instr{Op: OpStoreInt, Arg: slot} }
func LoadInt(slot int) Instr     { return Instr{Op: OpLoadInt, Arg: slot} }
func Pop() Instr                 { return Instr{Op: OpPop} }
func Goto(target int) Instr      { return Instr{Op: OpGoto, Arg: target} }
func Branch(a, b int) Instr      { return Instr{Op: OpBranch, Arg: a, Arg2: b} }
func Return() Instr              { return Instr{Op: OpReturn} }

// 语言级硬限制。
const (
	MaxSlots      = 32 // 局部槽数上限
	MaxStackDepth = 32 // 操作数栈深上限
	MaxCallDepth  = 4  // 子程序嵌套上限
)

// Options 配置验证器；零值取默认。
type Options struct {
	NumSlots int // 局部槽数，<= MaxSlots，默认 32
	Budget   int // 分析步数预算，默认 100000
}

// Verdict 是验证结论。
type Verdict int

const (
	Safe     Verdict = iota // 所有可达路径均通过
	Rejected                // 发现违规，Rejection 含拒绝前的调用点链
	Unknown                 // 预算耗尽，不能给出可加载结论
)

func (v Verdict) String() string {
	switch v {
	case Safe:
		return "Safe"
	case Rejected:
		return "Rejected"
	default:
		return "Unknown"
	}
}

// Rejection 描述一次拒绝，含拒绝点的调用点链（jsr 指令位置序列）。
type Rejection struct {
	PC        int
	Reason    string
	CallChain []int
}

func (r *Rejection) String() string {
	chain := "<方法入口>"
	if len(r.CallChain) > 0 {
		parts := make([]string, len(r.CallChain))
		for i, site := range r.CallChain {
			parts[i] = fmt.Sprintf("jsr@%d", site)
		}
		chain = strings.Join(parts, " -> ")
	}
	return fmt.Sprintf("拒绝于 pc=%d（调用点链 %s）：%s", r.PC, chain, r.Reason)
}

// RetEdge 记录分析中确认的一条 ret 返回边，用于检验上下文敏感精度。
type RetEdge struct {
	CallChain []int // ret 所在的调用点链
	RetPC     int   // ret 指令位置
	Target    int   // 精确返回位置（调用点的下一条指令）
}

// Result 是验证结果。
type Result struct {
	Verdict   Verdict
	Rejection *Rejection // Verdict 为 Rejected 时非空
	RetEdges  []RetEdge
	Explored  int // 实际消耗的分析步数
}

// 抽象值：整数、返回令牌（记录所属调用帧深度）、失效令牌、不可用（合流不相容）。
type valKind int

const (
	kindUnavailable valKind = iota
	kindInt
	kindToken
	kindInvalidToken
)

type val struct {
	kind  valKind
	frame int // kindToken / kindInvalidToken：所属调用帧在调用点链中的深度
}

type state struct {
	locals []val
	stack  []val
}

func (s state) clone() state {
	out := state{locals: make([]val, len(s.locals)), stack: make([]val, len(s.stack))}
	copy(out.locals, s.locals)
	copy(out.stack, s.stack)
	return out
}

type workItem struct {
	pc  int
	ctx []int
	st  state
}

type verifier struct {
	code     []Instr
	numSlots int
	budget   int
	explored int

	states   map[string]state
	worklist []workItem

	rejection *Rejection
	retEdges  []RetEdge
}

// Verify 对指令数组做上下文敏感的抽象解释，分析上下文以调用点栈区分。
func Verify(code []Instr, opts Options) Result {
	numSlots := opts.NumSlots
	if numSlots <= 0 {
		numSlots = MaxSlots
	}
	budget := opts.Budget
	if budget <= 0 {
		budget = 100000
	}
	v := &verifier{
		code:     code,
		numSlots: numSlots,
		budget:   budget,
		states:   make(map[string]state),
	}
	return v.run()
}

func (v *verifier) run() Result {
	if len(v.code) == 0 {
		return Result{Verdict: Safe}
	}
	v.merge(0, nil, state{locals: make([]val, v.numSlots)})
	for len(v.worklist) > 0 && v.rejection == nil {
		if v.explored >= v.budget {
			return Result{Verdict: Unknown, RetEdges: v.retEdges, Explored: v.explored}
		}
		item := v.worklist[len(v.worklist)-1]
		v.worklist = v.worklist[:len(v.worklist)-1]
		v.explored++
		v.step(item.pc, item.ctx, item.st)
	}
	if v.rejection != nil {
		return Result{Verdict: Rejected, Rejection: v.rejection, RetEdges: v.retEdges, Explored: v.explored}
	}
	return Result{Verdict: Safe, RetEdges: v.retEdges, Explored: v.explored}
}

func ctxKey(pc int, ctx []int) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%d|", pc)
	for i, site := range ctx {
		if i > 0 {
			b.WriteByte(',')
		}
		fmt.Fprintf(&b, "%d", site)
	}
	return b.String()
}

// merge 把新状态合入 (pc, ctx) 的已知状态：局部槽不相容变不可用，
// 操作数栈不相容直接拒绝。
func (v *verifier) merge(pc int, ctx []int, st state) {
	if v.rejection != nil {
		return
	}
	if pc < 0 || pc >= len(v.code) {
		v.reject(pc, ctx, "控制流越出指令数组")
		return
	}
	key := ctxKey(pc, ctx)
	old, ok := v.states[key]
	if !ok {
		v.states[key] = st
		v.worklist = append(v.worklist, workItem{pc: pc, ctx: append([]int(nil), ctx...), st: st})
		return
	}
	if covers(old, st) {
		return
	}
	if !stackEqual(old.stack, st.stack) {
		v.reject(pc, ctx, "合流时操作数栈不相容")
		return
	}
	joined := state{locals: make([]val, v.numSlots), stack: old.stack}
	for i := range joined.locals {
		if old.locals[i] == st.locals[i] {
			joined.locals[i] = old.locals[i]
		} else {
			joined.locals[i] = val{kind: kindUnavailable}
		}
	}
	v.states[key] = joined
	v.worklist = append(v.worklist, workItem{pc: pc, ctx: append([]int(nil), ctx...), st: joined})
}

// covers 判断 old 是否已覆盖 newer（局部槽方向：不可用覆盖一切具体值）。
func covers(old, newer state) bool {
	if !stackEqual(old.stack, newer.stack) {
		return false
	}
	for i := range old.locals {
		if old.locals[i] != newer.locals[i] && old.locals[i].kind != kindUnavailable {
			return false
		}
	}
	return true
}

func stackEqual(a, b []val) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func (v *verifier) reject(pc int, ctx []int, reason string) {
	if v.rejection != nil {
		return
	}
	v.rejection = &Rejection{PC: pc, Reason: reason, CallChain: append([]int(nil), ctx...)}
}

func (v *verifier) step(pc int, ctx []int, st state) {
	ins := v.code[pc]
	next := pc + 1
	switch ins.Op {
	case OpJsr:
		target := ins.Arg
		if target < 0 || target >= len(v.code) {
			v.reject(pc, ctx, "jsr 目标越出指令数组")
			return
		}
		if len(ctx) >= MaxCallDepth {
			v.reject(pc, ctx, fmt.Sprintf("子程序嵌套超过 %d 层", MaxCallDepth))
			return
		}
		for _, site := range ctx {
			if v.code[site].Op == OpJsr && v.code[site].Arg == target {
				v.reject(pc, ctx, "活动调用中再次进入同一子程序，视为递归")
				return
			}
		}
		if len(st.stack) >= MaxStackDepth {
			v.reject(pc, ctx, "操作数栈溢出")
			return
		}
		ns := st.clone()
		// 返回令牌仅属于本次调用：帧深度为新的最内层，值为 pc+1（由调用点链推出）。
		ns.stack = append(ns.stack, val{kind: kindToken, frame: len(ctx)})
		v.merge(target, append(append([]int(nil), ctx...), pc), ns)

	case OpRet:
		slot := ins.Arg
		if !v.checkSlot(pc, ctx, slot) {
			return
		}
		if len(ctx) == 0 {
			v.reject(pc, ctx, "ret 时无活动调用")
			return
		}
		lv := st.locals[slot]
		switch lv.kind {
		case kindUnavailable:
			v.reject(pc, ctx, "ret 读取的局部槽不可用（合流不相容）")
			return
		case kindInt:
			v.reject(pc, ctx, "ret 读取到整数，整数与返回令牌不可转换")
			return
		case kindInvalidToken:
			v.reject(pc, ctx, "ret 读取到失效的返回令牌")
			return
		}
		inner := len(ctx) - 1
		if lv.frame != inner {
			v.reject(pc, ctx, "ret 读取的返回令牌不属于当前最内层调用")
			return
		}
		retAddr := ctx[inner] + 1
		ns := st.clone()
		invalidateFrame(ns.locals, inner)
		invalidateFrame(ns.stack, inner)
		v.retEdges = append(v.retEdges, RetEdge{
			CallChain: append([]int(nil), ctx...),
			RetPC:     pc,
			Target:    retAddr,
		})
		v.merge(retAddr, ctx[:inner], ns)

	case OpStoreReturn:
		slot := ins.Arg
		if !v.checkSlot(pc, ctx, slot) {
			return
		}
		if len(st.stack) == 0 {
			v.reject(pc, ctx, "storeReturn 时操作数栈为空")
			return
		}
		top := st.stack[len(st.stack)-1]
		if top.kind != kindToken {
			v.reject(pc, ctx, "storeReturn 需要返回令牌，整数与返回令牌不可转换")
			return
		}
		ns := st.clone()
		ns.stack = ns.stack[:len(ns.stack)-1]
		ns.locals[slot] = top
		v.merge(next, ctx, ns)

	case OpLoadReturn:
		slot := ins.Arg
		if !v.checkSlot(pc, ctx, slot) {
			return
		}
		lv := st.locals[slot]
		switch lv.kind {
		case kindUnavailable:
			v.reject(pc, ctx, "loadReturn 读取的局部槽不可用（合流不相容）")
			return
		case kindInt:
			v.reject(pc, ctx, "loadReturn 读取到整数，整数与返回令牌不可转换")
			return
		case kindInvalidToken:
			v.reject(pc, ctx, "loadReturn 加载失效的返回令牌")
			return
		}
		if len(st.stack) >= MaxStackDepth {
			v.reject(pc, ctx, "操作数栈溢出")
			return
		}
		ns := st.clone()
		ns.stack = append(ns.stack, lv)
		v.merge(next, ctx, ns)

	case OpPushInt:
		if len(st.stack) >= MaxStackDepth {
			v.reject(pc, ctx, "操作数栈溢出")
			return
		}
		ns := st.clone()
		ns.stack = append(ns.stack, val{kind: kindInt})
		v.merge(next, ctx, ns)

	case OpStoreInt:
		slot := ins.Arg
		if !v.checkSlot(pc, ctx, slot) {
			return
		}
		if len(st.stack) == 0 {
			v.reject(pc, ctx, "storeInt 时操作数栈为空")
			return
		}
		top := st.stack[len(st.stack)-1]
		if top.kind != kindInt {
			v.reject(pc, ctx, "storeInt 需要整数，整数与返回令牌不可转换")
			return
		}
		ns := st.clone()
		ns.stack = ns.stack[:len(ns.stack)-1]
		ns.locals[slot] = val{kind: kindInt}
		v.merge(next, ctx, ns)

	case OpLoadInt:
		slot := ins.Arg
		if !v.checkSlot(pc, ctx, slot) {
			return
		}
		lv := st.locals[slot]
		switch lv.kind {
		case kindUnavailable:
			v.reject(pc, ctx, "loadInt 读取的局部槽不可用（合流不相容）")
			return
		case kindToken:
			v.reject(pc, ctx, "loadInt 读取到返回令牌，整数与返回令牌不可转换")
			return
		case kindInvalidToken:
			v.reject(pc, ctx, "loadInt 读取到失效的返回令牌")
			return
		}
		if len(st.stack) >= MaxStackDepth {
			v.reject(pc, ctx, "操作数栈溢出")
			return
		}
		ns := st.clone()
		ns.stack = append(ns.stack, val{kind: kindInt})
		v.merge(next, ctx, ns)

	case OpPop:
		if len(st.stack) == 0 {
			v.reject(pc, ctx, "pop 时操作数栈为空")
			return
		}
		ns := st.clone()
		ns.stack = ns.stack[:len(ns.stack)-1]
		v.merge(next, ctx, ns)

	case OpGoto:
		v.merge(ins.Arg, ctx, st)

	case OpBranch:
		v.merge(ins.Arg, ctx, st)
		v.merge(ins.Arg2, ctx, st)

	case OpReturn:
		if len(ctx) != 0 {
			v.reject(pc, ctx, "方法 return 时活动调用栈非空")
			return
		}
		if len(st.stack) != 0 {
			v.reject(pc, ctx, "方法 return 时操作数栈非空")
			return
		}

	default:
		v.reject(pc, ctx, "未知指令")
	}
}

func (v *verifier) checkSlot(pc int, ctx []int, slot int) bool {
	if slot < 0 || slot >= v.numSlots {
		v.reject(pc, ctx, "局部槽越界")
		return false
	}
	return true
}

// invalidateFrame 使指定调用帧的返回令牌所有别名失效。
func invalidateFrame(vals []val, frame int) {
	for i, x := range vals {
		if x.kind == kindToken && x.frame == frame {
			vals[i] = val{kind: kindInvalidToken, frame: frame}
		}
	}
}
