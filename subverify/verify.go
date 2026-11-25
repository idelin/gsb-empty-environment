// Package subverify 实现上下文敏感的子程序（jsr/ret）字节码验证。
//
// 旧字节码里共享清理子程序通常把返回位置保存在局部槽中，恶意代码可以把
// 别处的返回位置搬入槽中劫持控制流。本包把每次 jsr 铸造的返回令牌绑定到
// “仅属于本次调用”的调用点链上：ret 只接受当前最内层调用铸造的令牌，返回
// 后该令牌在所有局部槽、操作数栈中的别名与派生拷贝一律失效，失效令牌不可
// 加载、不可复用。分析上下文按调用点栈区分，两个调用方的返回位置永远不会
// 被并成同一个可跳转目标。
package subverify

import (
	"fmt"
	"strconv"
	"strings"
)

// Op 是验证器支持的操作码。
type Op uint8

const (
	OpInvalid     Op = iota
	OpJsr            // Arg = 子程序入口指令位置
	OpRet            // Arg = 保存返回令牌的局部槽
	OpStoreReturn    // Arg = 局部槽；弹出栈顶返回令牌存入
	OpLoadReturn     // Arg = 局部槽；把返回令牌压栈
	OpPushInt        // 压入一个抽象整数
	OpStoreInt       // Arg = 局部槽；弹出栈顶整数存入
	OpLoadInt        // Arg = 局部槽；把整数压栈
	OpPop            // 弹出栈顶
	OpGoto           // Arg = 跳转目标
	OpBranch         // 非确定二分支：后继为 Arg 与 pc+1
	OpReturn         // 方法返回
)

// Instr 表示一条指令。
type Instr struct {
	Op  Op
	Arg int
}

// Program 是待验证的指令数组及其资源上限。NumSlots 与 MaxStack 均不得超过 32。
type Program struct {
	Code     []Instr
	NumSlots int
	MaxStack int
}

// Conclusion 是验证结论。
type Conclusion int

const (
	// ConclusionReject 表示程序确定不可加载。
	ConclusionReject Conclusion = iota
	// ConclusionAccept 表示程序通过全部安全检查。
	ConclusionAccept
	// ConclusionUnknown 表示分析预算耗尽，不得当作可加载。
	ConclusionUnknown
)

// Result 携带验证结论；拒绝时 CallChain 是拒绝发生前活动的 jsr 调用点链
// （根调用在前，最内层调用在后），AtPC 是触发拒绝的指令位置。
type Result struct {
	Conclusion Conclusion
	AtPC       int
	Reason     string
	CallChain  []int
}

// CallChainText 以 “jsr@2 -> jsr@5” 的形式渲染调用点链；无活动调用时返回 <root>。
func (r Result) CallChainText() string {
	if len(r.CallChain) == 0 {
		return "<root>"
	}
	parts := make([]string, len(r.CallChain))
	for i, pc := range r.CallChain {
		parts[i] = "jsr@" + strconv.Itoa(pc)
	}
	return strings.Join(parts, " -> ")
}

func (r Result) String() string {
	switch r.Conclusion {
	case ConclusionAccept:
		return "accept"
	case ConclusionUnknown:
		return fmt.Sprintf("unknown at pc=%d: %s", r.AtPC, r.Reason)
	default:
		return fmt.Sprintf("reject at pc=%d via %s: %s", r.AtPC, r.CallChainText(), r.Reason)
	}
}

const (
	maxSlots      = 32
	maxStackDeep  = 32
	maxCallDepth  = 4
	defaultBudget = 200000
)

type valueKind uint8

const (
	// vTop 表示未初始化或合流后不相容、已不可用的槽/格。
	vTop valueKind = iota
	vInt
	vReturnToken
	// vDeadToken 表示曾是返回令牌、但已随对应 ret 失效的值。
	vDeadToken
)

// slotValue 是局部槽与操作数栈格的抽象值。owner 仅对 vReturnToken 有效，
// 记录铸造该令牌的调用点链（如 "2/5"）。
type slotValue struct {
	kind  valueKind
	owner string
}

// callFrame 是一次 jsr 调用的分析帧。savedStack 是 jsr 瞬间的操作数栈快照，
// ret 时当前栈必须与之相容，防止令牌或其他值借栈跨调用逃逸。
type callFrame struct {
	sitePC     int
	entryPC    int
	returnPC   int
	savedStack []slotValue
}

type verifState struct {
	locals []slotValue
	stack  []slotValue
	frames []callFrame
}

func (s *verifState) clone() *verifState {
	cp := &verifState{
		locals: append([]slotValue(nil), s.locals...),
		stack:  append([]slotValue(nil), s.stack...),
		frames: make([]callFrame, len(s.frames)),
	}
	for i, f := range s.frames {
		cp.frames[i] = callFrame{
			sitePC:     f.sitePC,
			entryPC:    f.entryPC,
			returnPC:   f.returnPC,
			savedStack: append([]slotValue(nil), f.savedStack...),
		}
	}
	return cp
}

func chainText(frames []callFrame) string {
	parts := make([]string, len(frames))
	for i, f := range frames {
		parts[i] = strconv.Itoa(f.sitePC)
	}
	return strings.Join(parts, "/")
}

func extendChain(chain string, sitePC int) string {
	site := strconv.Itoa(sitePC)
	if chain == "" {
		return site
	}
	return chain + "/" + site
}

// liveAt 判断 owner 调用点链是否仍是当前调用栈上的活动调用：相等或是其前缀。
func liveAt(owner, currentChain string) bool {
	return owner == currentChain || strings.HasPrefix(currentChain, owner+"/")
}

func valuesEqual(a, b slotValue) bool {
	return a.kind == b.kind && a.owner == b.owner
}

// mergeValue 实现局部槽合流：相容取公共类型，相容信息丢失则返回 changed。
// 不相容降级为不可用的 vTop。
func mergeValue(a, b slotValue) (slotValue, bool) {
	if valuesEqual(a, b) {
		return a, false
	}
	switch {
	case a.kind == vTop || b.kind == vTop:
		return slotValue{kind: vTop}, true
	case a.kind == vInt && b.kind == vInt:
		return a, false
	case a.kind == vDeadToken && b.kind == vDeadToken:
		return slotValue{kind: vDeadToken}, true
	default:
		// 整数与令牌、不同调用的令牌、令牌与失效令牌等均不相容。
		return slotValue{kind: vTop}, true
	}
}

// mergeStack 合流操作数栈；栈深不同或同位元素不相容即判定整个合流拒绝。
func mergeStack(dst, src []slotValue) (changed bool, incompatible bool) {
	if len(dst) != len(src) {
		return false, true
	}
	for i := range dst {
		merged, ch := mergeValue(dst[i], src[i])
		if ch {
			changed = true
		}
		dst[i] = merged
	}
	return changed, false
}

// mergeState 把 src 并入 dst。changed 表示格发生下降、需要重新分析；
// incompatible 表示调用链、操作数栈或调用快照不相容，必须拒绝程序。
func mergeState(dst, src *verifState) (changed bool, incompatible bool) {
	if len(dst.frames) != len(src.frames) {
		return false, true
	}
	for i := range dst.frames {
		if dst.frames[i].sitePC != src.frames[i].sitePC {
			return false, true
		}
		ch, bad := mergeStack(dst.frames[i].savedStack, src.frames[i].savedStack)
		if bad {
			return false, true
		}
		changed = changed || ch
	}
	for i := range dst.locals {
		merged, ch := mergeValue(dst.locals[i], src.locals[i])
		dst.locals[i] = merged
		changed = changed || ch
	}
	ch, bad := mergeStack(dst.stack, src.stack)
	if bad {
		return false, true
	}
	return changed || ch, false
}

type successor struct {
	pc    int
	chain string
	state *verifState
}

type verifier struct {
	prog   *Program
	budget int
}

func (v *verifier) reject(pc int, st *verifState, format string, args ...any) Result {
	chain := make([]int, 0, len(st.frames))
	for _, f := range st.frames {
		chain = append(chain, f.sitePC)
	}
	return Result{
		Conclusion: ConclusionReject,
		AtPC:       pc,
		Reason:     fmt.Sprintf(format, args...),
		CallChain:  chain,
	}
}

func (v *verifier) unknown(pc int, st *verifState) Result {
	chain := make([]int, 0, len(st.frames))
	for _, f := range st.frames {
		chain = append(chain, f.sitePC)
	}
	return Result{
		Conclusion: ConclusionUnknown,
		AtPC:       pc,
		Reason:     "分析预算耗尽，结论未知，不得判定为可加载",
		CallChain:  chain,
	}
}

func (v *verifier) validTarget(pc int) bool {
	return pc >= 0 && pc < len(v.prog.Code)
}

// Verify 使用默认分析预算验证程序。
func Verify(p *Program) Result {
	return VerifyWithBudget(p, defaultBudget)
}

// VerifyWithBudget 以给定指令处理预算验证程序；预算耗尽返回 ConclusionUnknown。
func VerifyWithBudget(p *Program, budget int) Result {
	if p == nil {
		return Result{Conclusion: ConclusionReject, Reason: "空程序"}
	}
	if len(p.Code) == 0 {
		return Result{Conclusion: ConclusionReject, Reason: "指令数组为空"}
	}
	if p.NumSlots <= 0 || p.NumSlots > maxSlots {
		return Result{Conclusion: ConclusionReject, Reason: fmt.Sprintf("局部槽数必须在 1..%d 之间", maxSlots)}
	}
	if p.MaxStack <= 0 || p.MaxStack > maxStackDeep {
		return Result{Conclusion: ConclusionReject, Reason: fmt.Sprintf("操作数栈深度必须在 1..%d 之间", maxStackDeep)}
	}
	if budget <= 0 {
		budget = defaultBudget
	}

	v := &verifier{prog: p, budget: budget}
	initial := &verifState{
		locals: make([]slotValue, p.NumSlots),
		stack:  make([]slotValue, 0, p.MaxStack),
	}

	type workItem struct {
		pc    int
		chain string
	}
	stateKey := func(pc int, chain string) string {
		return strconv.Itoa(pc) + "|" + chain
	}
	states := map[string]*verifState{stateKey(0, ""): initial}
	worklist := []workItem{{pc: 0, chain: ""}}

	for len(worklist) > 0 {
		item := worklist[0]
		worklist = worklist[1:]
		st := states[stateKey(item.pc, item.chain)].clone()

		v.budget--
		if v.budget < 0 {
			return v.unknown(item.pc, st)
		}

		succs, res := v.transfer(item.pc, st)
		if res.Conclusion == ConclusionReject {
			return res
		}
		for _, sc := range succs {
			key := stateKey(sc.pc, sc.chain)
			old, ok := states[key]
			if !ok {
				states[key] = sc.state
				worklist = append(worklist, workItem{pc: sc.pc, chain: sc.chain})
				continue
			}
			changed, bad := mergeState(old, sc.state)
			if bad {
				return v.reject(sc.pc, sc.state, "合流点调用链、操作数栈或调用快照不相容")
			}
			if changed {
				worklist = append(worklist, workItem{pc: sc.pc, chain: sc.chain})
			}
		}
	}
	return Result{Conclusion: ConclusionAccept}
}
