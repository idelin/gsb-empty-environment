package sccp

import "fmt"

// ConstReplacement 描述一处可做的常量替换：在某程序点上，
// 变量 Var 被证明恒等于 Value。
type ConstReplacement struct {
	BlockID int
	Block   string
	Instr   int    // 指令下标；-1 表示终结符的分支条件
	Slot    string // "A"、"B" 或 "Cond"
	Var     string
	Value   uint8
}

// EdgeDeletion 描述一条被证明可删除的边及其理由。
// 只有源块本身可达时，分析才会给出删边结论。
type EdgeDeletion struct {
	FromID, ToID int
	From, To     string
	Reason       string
}

// UnresolvedEdge 描述一条无法下结论的边：其源块从未被分析到达。
// 这类边不是"被证明永不可达"，只是"尚未访问"，因此不进入删边集合。
type UnresolvedEdge struct {
	FromID, ToID int
	From, To     string
	Reason       string
}

// Report 是 SCCP 的全部输出。
type Report struct {
	Replacements      []ConstReplacement
	DeletedEdges      []EdgeDeletion
	Unresolved        []UnresolvedEdge
	UnreachableBlocks []string
}

// Report 在不动点上生成常量替换与删边建议。
//
// 关键不变式：只有"源块可达"的边才可能被判定删除；
// 源块不可达的边一律进入 Unresolved，避免把尚未访问的边
// 误判为永不可达。
func (a *Analyzer) Report() *Report {
	rep := &Report{}
	for _, b := range a.cfg.Blocks {
		if !a.Reachable[b.ID] {
			rep.UnreachableBlocks = append(rep.UnreachableBlocks, b.Name)
			for _, e := range a.cfg.Successors(b.ID) {
				rep.Unresolved = append(rep.Unresolved, UnresolvedEdge{
					FromID: e.From, ToID: e.To,
					From: b.Name, To: a.cfg.block(e.To).Name,
					Reason: "源块从入口不可达，该边从未被分析访问，不能判定为永不可达",
				})
			}
			continue
		}

		// 重放块内传递，收集每个程序点上的常量操作数。
		st := copyEnv(a.Env[b.ID])
		trapIdx := -1
		for i, ins := range b.Instrs {
			rep.collectOperand(b, i, "A", ins.A, st)
			rep.collectOperand(b, i, "B", ins.B, st)
			res, trap := transfer(ins, st)
			if trap && trapIdx < 0 {
				trapIdx = i
			}
			st[ins.Dst] = res
		}

		// 终结符条件的常量替换。
		if b.Term.Kind == TermBranch {
			rep.collectOperand(b, -1, "Cond", b.Term.Cond, st)
		}

		// 删边判定。
		succs := a.cfg.Successors(b.ID)
		switch {
		case trapIdx >= 0:
			// 块在终结符之前必然 trap，所有出边均不可执行。
			for _, e := range succs {
				rep.DeletedEdges = append(rep.DeletedEdges, EdgeDeletion{
					FromID: e.From, ToID: e.To,
					From: b.Name, To: a.cfg.block(e.To).Name,
					Reason: fmt.Sprintf("块内第 %d 条指令除数为常量 0，必然触发除零异常，控制流无法到达终结符", trapIdx),
				})
			}
		case b.Term.Kind == TermBranch:
			cv := evalOperand(b.Term.Cond, st)
			if cv.Kind == Const {
				dead := b.Term.False
				if cv.Val == 0 {
					dead = b.Term.True
				}
				if dead != cvTarget(b, cv.Val) {
					rep.DeletedEdges = append(rep.DeletedEdges, EdgeDeletion{
						FromID: b.ID, ToID: dead,
						From: b.Name, To: a.cfg.block(dead).Name,
						Reason: fmt.Sprintf("分支条件为常量 %d，该方向永不可达", cv.Val),
					})
				}
			}
		}
	}
	return rep
}

// cvTarget 返回常量条件对应的跳转目标。
func cvTarget(b *Block, cond uint8) int {
	if cond != 0 {
		return b.Term.True
	}
	return b.Term.False
}

// collectOperand 若操作数是已被证明为常量的变量，则记录一条替换。
func (r *Report) collectOperand(b *Block, instr int, slot string, op Operand, st map[string]Value) {
	if op.IsConst {
		return
	}
	if v, ok := st[op.Var]; ok && v.Kind == Const {
		r.Replacements = append(r.Replacements, ConstReplacement{
			BlockID: b.ID, Block: b.Name,
			Instr: instr, Slot: slot,
			Var: op.Var, Value: v.Val,
		})
	}
}
