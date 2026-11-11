package sanitsum

import (
	"fmt"
	"sort"
)

// analyzer 持有一次推导的共享上下文。
type analyzer struct {
	g       *FuncGraph
	blocks  map[string]*Block
	full    uint64
	fields  map[fieldKey]struct{} // 图中出现过的全部副作用字段槽位
	rawRets map[RetVal]*rawRet
}

// rawRet 是返回值摘要的内部形态，保存“已清除位集”，跨路径取交集。
type rawRet struct {
	paramClean  map[string]uint64
	resultClean map[string]uint64
	fieldClean  map[fieldKey]uint64
}

func newAnalyzer(g *FuncGraph) (*analyzer, error) {
	if err := validate(g); err != nil {
		return nil, err
	}
	a := &analyzer{
		g:       g,
		blocks:  make(map[string]*Block, len(g.Blocks)),
		full:    allBits(len(g.Categories)),
		fields:  make(map[fieldKey]struct{}),
		rawRets: make(map[RetVal]*rawRet),
	}
	for _, b := range g.Blocks {
		a.blocks[b.ID] = b
		for _, st := range b.Stmts {
			switch t := st.(type) {
			case SanitizeStmt:
				if t.Target.Field != "" {
					a.fields[fieldKey{t.Target.Var, t.Target.Field}] = struct{}{}
				}
			case StoreStmt:
				a.fields[fieldKey{t.Dst.Var, t.Dst.Field}] = struct{}{}
			}
		}
	}
	return a, nil
}

// Analyze 用 DAG 数据流（汇合点按位并集、返回点按位交集）推导函数摘要。
func Analyze(g *FuncGraph) (*Summary, error) {
	a, err := newAnalyzer(g)
	if err != nil {
		return nil, err
	}
	order, err := a.topoOrder()
	if err != nil {
		return nil, err
	}
	in := make(map[string]*state, len(order))
	in[g.Entry] = entryState(g, a.fields)
	for _, id := range order {
		b := a.blocks[id]
		cur, ok := in[id]
		if !ok {
			return nil, fmt.Errorf("sanitsum: 块 %q 从入口 %q 不可达", id, g.Entry)
		}
		terminal := a.transferBlock(b, cur)
		for _, succ := range b.Succs {
			if existing, ok := in[succ]; ok {
				existing.mergeIn(cur)
			} else {
				in[succ] = cur.clone()
			}
		}
		_ = terminal
	}
	return a.finalize(), nil
}

// EnumeratePaths 枚举无环图上的每条完整路径，分别在返回点取交集。
// 它与 Analyze 的结果应当一致，作为摘要正确性的交叉校验实现。
func EnumeratePaths(g *FuncGraph) (*Summary, error) {
	a, err := newAnalyzer(g)
	if err != nil {
		return nil, err
	}
	if _, err := a.topoOrder(); err != nil {
		return nil, err
	}
	a.enumerate(a.blocks[g.Entry], entryState(g, a.fields))
	return a.finalize(), nil
}

func (a *analyzer) enumerate(b *Block, cur *state) {
	terminal := a.transferBlock(b, cur)
	if terminal != "" {
		return
	}
	for _, succ := range b.Succs {
		// 每条路径携带独立状态副本，路径之间不共享合并。
		a.enumerate(a.blocks[succ], cur.clone())
	}
}

// transferBlock 在块内顺序执行语句；遇到 ReturnStmt 时快照并返回该返回值。
func (a *analyzer) transferBlock(b *Block, cur *state) RetVal {
	for _, st := range b.Stmts {
		switch t := st.(type) {
		case SanitizeStmt:
			bit, err := catBit(a.g.Categories, t.Cat)
			if err != nil {
				continue // validate 已保证类别合法
			}
			cur.clearBit(t.Target, bit)
		case AssignStmt:
			cur.write(t.Dst, a.eval(t.RHS, cur))
		case StoreStmt:
			cur.write(t.Dst, a.eval(t.RHS, cur))
		case ReturnStmt:
			a.snapshot(t.Ret, cur)
			return t.Ret
		}
	}
	return ""
}

func (a *analyzer) eval(o RefOrConst, cur *state) uint64 {
	if o.IsConst() {
		return 0
	}
	return cur.read(o.Ref)
}

// snapshot 在返回点记录“已净化类别”，同一返回值的多条路径取交集。
func (a *analyzer) snapshot(rv RetVal, cur *state) {
	rr, ok := a.rawRets[rv]
	if !ok {
		rr = &rawRet{
			paramClean:  make(map[string]uint64),
			resultClean: make(map[string]uint64),
			fieldClean:  make(map[fieldKey]uint64),
		}
		a.rawRets[rv] = rr
	}
	for _, p := range a.g.Params {
		clean := a.full &^ cur.vars[p]
		intersectBit(rr.paramClean, p, clean)
	}
	for _, r := range a.g.Results {
		clean := a.full &^ cur.vars[r]
		intersectBit(rr.resultClean, r, clean)
	}
	for fk := range a.fields {
		clean := a.full &^ cur.fields[fk]
		intersectFieldBit(rr.fieldClean, fk, clean)
	}
}

func intersectBit(m map[string]uint64, k string, clean uint64) {
	if v, seen := m[k]; seen {
		m[k] = v & clean
	} else {
		m[k] = clean
	}
}

func intersectFieldBit(m map[fieldKey]uint64, k fieldKey, clean uint64) {
	if v, seen := m[k]; seen {
		m[k] = v & clean
	} else {
		m[k] = clean
	}
}

func (a *analyzer) finalize() *Summary {
	m := &Summary{
		Func:       a.g.Name,
		Categories: append([]Category(nil), a.g.Categories...),
		Params:     append([]string(nil), a.g.Params...),
		Results:    append([]string(nil), a.g.Results...),
		Returns:    make(map[RetVal]*RetSummary, len(a.rawRets)),
	}
	rvs := make([]RetVal, 0, len(a.rawRets))
	for rv := range a.rawRets {
		rvs = append(rvs, rv)
	}
	sort.Slice(rvs, func(i, j int) bool { return rvs[i] < rvs[j] })
	for _, rv := range rvs {
		rr := a.rawRets[rv]
		rs := &RetSummary{
			ParamClean:  make(map[string]CatSet, len(rr.paramClean)),
			ResultClean: make(map[string]CatSet, len(rr.resultClean)),
			FieldClean:  make(map[Ref]CatSet, len(rr.fieldClean)),
		}
		for k, v := range rr.paramClean {
			rs.ParamClean[k] = CatSet{bits: v}
		}
		for k, v := range rr.resultClean {
			rs.ResultClean[k] = CatSet{bits: v}
		}
		for fk, v := range rr.fieldClean {
			rs.FieldClean[Ref{Var: fk.varName, Field: fk.field}] = CatSet{bits: v}
		}
		m.Returns[rv] = rs
	}
	return m
}
