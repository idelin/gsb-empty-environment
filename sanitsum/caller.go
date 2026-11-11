package sanitsum

import (
	"fmt"
	"sort"
)

// CallerStmt 是调用方图中的语句。
type CallerStmt interface {
	callerStmtTag()
}

// TaintStmt 把指定污染类别引入 Dst（模拟污染源）。
type TaintStmt struct {
	Dst Ref
	Cat Category
}

// CallStmt 调用一个已有摘要的库函数。
//   - Args：库参数名 -> 调用方实参 Ref；
//   - RetRefs：库结果 i -> 调用方接收返回值的 Ref，长度可小于库结果数，
//     缺省或留空 Ref 表示该返回值被调用方忽略。
type CallStmt struct {
	Callee  *Summary
	Args    map[string]Ref
	RetRefs []Ref
}

// BranchOnStmt 依据第 CallIndex 个调用（同块内 CallStmt 的序号）的守卫
// 返回值做分支。Expect 是当前块选择的返回值，Edges 给出各返回值的后继块；
// 取反条件只需把 Expect 换成相反的 RetVal。
type BranchOnStmt struct {
	CallIndex int
	Expect    RetVal
	Edges     map[RetVal]string
}

// SinkStmt 检查 Target 是否仍携带 Cat 污染；携带则产出 Finding。
type SinkStmt struct {
	Target Ref
	Cat    Category
	Label  string
}

func (TaintStmt) callerStmtTag()    {}
func (CallStmt) callerStmtTag()     {}
func (BranchOnStmt) callerStmtTag() {}
func (SinkStmt) callerStmtTag()     {}

// CallerBlock 是调用方图的基本块。
type CallerBlock struct {
	ID    string
	Stmts []CallerStmt
	Succs []string
}

// CallerGraph 是无循环的调用方控制流图。
type CallerGraph struct {
	Name       string
	Categories []Category
	Entry      string
	Blocks     []*CallerBlock
}

// Finding 是一处可能的注入：污染在未被必然净化的情况下到达 sink。
type Finding struct {
	Block  string
	Sink   string
	Target Ref
	Cat    Category
	Reason string
}

// pendingClean 保存一次调用在各返回值下“有条件成立”的净化位集，
// 等待 BranchOnStmt 按实际分支应用；调用被忽略时这些条件净化永不生效。
type pendingClean struct {
	slots map[Ref]map[RetVal]uint64
}

type callerAnalyzer struct {
	g        *CallerGraph
	blocks   map[string]*CallerBlock
	full     uint64
	effects  map[string]*callEffect
	findings map[Finding]struct{}
}

// frame 是调用方 DAG 上一个块入口处的传播帧：变量污染状态与挂起的条件净化。
type frame struct {
	st      *state
	pending map[string]*pendingClean
}

// callEffect 是库摘要在调用方位集布局下的预编译形态。
type callEffect struct {
	rv []RetVal
	// 无条件成立的净化：所有返回值下都成立的交集。
	guaranteed map[Ref]uint64
	// 各返回值下的条件净化（已包含无条件部分）。
	perRet map[RetVal]map[Ref]uint64
	// 结果变量初始污染来自实参污染的并集。
	argRefs []Ref
	retRefs []Ref
}

// AnalyzeCaller 在调用方图上传播污染并报告注入 Finding（按稳定顺序去重）。
func AnalyzeCaller(g *CallerGraph) ([]Finding, error) {
	if err := validateCaller(g); err != nil {
		return nil, err
	}
	a := &callerAnalyzer{
		g:        g,
		blocks:   make(map[string]*CallerBlock, len(g.Blocks)),
		full:     allBits(len(g.Categories)),
		effects:  make(map[string]*callEffect),
		findings: make(map[Finding]struct{}),
	}
	for _, b := range g.Blocks {
		a.blocks[b.ID] = b
		callIdx := -1
		for i := range b.Stmts {
			if call, ok := b.Stmts[i].(CallStmt); ok {
				callIdx++
				eff, err := buildEffect(&call, g.Categories)
				if err != nil {
					return nil, fmt.Errorf("块 %q: %w", b.ID, err)
				}
				a.effects[pendingKey(b.ID, callIdx)] = eff
			}
		}
	}
	order, err := a.topoOrder()
	if err != nil {
		return nil, err
	}
	in := map[string]*frame{g.Entry: {
		st:      a.entryState(),
		pending: make(map[string]*pendingClean),
	}}
	for _, id := range order {
		b := a.blocks[id]
		fr, ok := in[id]
		if !ok {
			return nil, fmt.Errorf("sanitsum: 调用方块 %q 从入口 %q 不可达", id, g.Entry)
		}
		callIdx := -1
		for _, st := range b.Stmts {
			switch t := st.(type) {
			case TaintStmt:
				bit, _ := catBit(g.Categories, t.Cat)
				fr.st.write(t.Dst, fr.st.read(t.Dst)|bit)
			case CallStmt:
				callIdx++
				a.applyCall(b.ID, t, fr, callIdx)
			case BranchOnStmt:
				if err := a.applyBranch(b.ID, t, fr); err != nil {
					return nil, fmt.Errorf("块 %q: %w", b.ID, err)
				}
			case SinkStmt:
				bit, _ := catBit(g.Categories, t.Cat)
				if fr.st.read(t.Target)&bit != 0 {
					a.findings[Finding{
						Block:  b.ID,
						Sink:   t.Label,
						Target: t.Target,
						Cat:    t.Cat,
						Reason: "污染类别 " + string(t.Cat) + " 在到达 sink 前未被必然净化",
					}] = struct{}{}
				}
			}
		}
		for _, succ := range b.Succs {
			next := &frame{st: fr.st.clone(), pending: clonePending(fr.pending)}
			if existing, ok := in[succ]; ok {
				existing.st.mergeIn(next.st)
				mergePending(existing.pending, next.pending)
			} else {
				in[succ] = next
			}
		}
	}
	out := make([]Finding, 0, len(a.findings))
	for f := range a.findings {
		out = append(out, f)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Block != out[j].Block {
			return out[i].Block < out[j].Block
		}
		if out[i].Sink != out[j].Sink {
			return out[i].Sink < out[j].Sink
		}
		return out[i].Target.String() < out[j].Target.String()
	})
	return out, nil
}

func (a *callerAnalyzer) entryState() *state {
	return &state{vars: make(map[string]uint64), fields: make(map[fieldKey]uint64)}
}

func (a *callerAnalyzer) applyCall(blockID string, t CallStmt, fr *frame, callIdx int) {
	eff := a.effects[pendingKey(blockID, callIdx)]
	// 结果变量的初始污染取实参污染并集；无实参时视为无污常量来源。
	var argTaint uint64
	for _, ref := range eff.argRefs {
		argTaint |= fr.st.read(ref)
	}
	for _, ref := range eff.retRefs {
		fr.st.write(ref, argTaint)
	}
	// 无条件净化（跨返回值交集）立即生效；这也是“忽略返回值”时唯一可用的保证。
	for ref, bits := range eff.guaranteed {
		fr.st.clearBit(ref, bits)
	}
	// 条件净化挂起，等待守卫分支；没有分支使用就自然失效。
	if len(eff.perRet) != 0 {
		fr.pending[pendingKey(blockID, callIdx)] = &pendingClean{
			slots: collectPendingSlots(eff),
		}
	}
}

func (a *callerAnalyzer) applyBranch(blockID string, t BranchOnStmt, fr *frame) error {
	pc, ok := fr.pending[pendingKey(blockID, t.CallIndex)]
	if !ok {
		return fmt.Errorf("BranchOnStmt 引用的调用序号 %d 不存在或已被消费", t.CallIndex)
	}
	perSlot, ok := pc.slotsFirst(t.Expect)
	if !ok {
		return fmt.Errorf("返回值 %q 不在库摘要中", t.Expect)
	}
	for ref, bits := range perSlot {
		fr.st.clearBit(ref, bits)
	}
	delete(fr.pending, pendingKey(blockID, t.CallIndex))
	return nil
}

func pendingKey(blockID string, callIdx int) string {
	return fmt.Sprintf("%s#%d", blockID, callIdx)
}

func (p *pendingClean) slotsFirst(rv RetVal) (map[Ref]uint64, bool) {
	out := make(map[Ref]uint64, len(p.slots))
	for ref, m := range p.slots {
		bits, ok := m[rv]
		if !ok {
			return nil, false
		}
		out[ref] = bits
	}
	return out, true
}

func collectPendingSlots(eff *callEffect) map[Ref]map[RetVal]uint64 {
	out := make(map[Ref]map[RetVal]uint64)
	for rv, slots := range eff.perRet {
		for ref, bits := range slots {
			if out[ref] == nil {
				out[ref] = make(map[RetVal]uint64)
			}
			out[ref][rv] = bits
		}
	}
	return out
}

func clonePending(in map[string]*pendingClean) map[string]*pendingClean {
	out := make(map[string]*pendingClean, len(in))
	for k, p := range in {
		cp := &pendingClean{slots: make(map[Ref]map[RetVal]uint64, len(p.slots))}
		for ref, m := range p.slots {
			mm := make(map[RetVal]uint64, len(m))
			for rv, bits := range m {
				mm[rv] = bits
			}
			cp.slots[ref] = mm
		}
		out[k] = cp
	}
	return out
}

// 汇合点只保留两条路径都仍挂起的条件净化：
// 任一路径已经按分支消费（删除）的保证，不能在合并后再次套用。
func mergePending(dst, src map[string]*pendingClean) {
	for k := range dst {
		if _, ok := src[k]; !ok {
			delete(dst, k)
		}
	}
}
