package sanitsum

import "fmt"

// buildEffect 把库摘要的类别位布局翻译到调用方类别顺序，并区分
// 无条件净化（跨返回值交集）与各返回值下的条件净化。
func buildEffect(call *CallStmt, callerCats []Category) (*callEffect, error) {
	s := call.Callee
	if s == nil {
		return nil, fmt.Errorf("CallStmt 缺少库摘要")
	}
	bitMap := make(map[Category]uint64, len(s.Categories))
	for _, c := range s.Categories {
		b, err := catBit(callerCats, c)
		if err != nil {
			return nil, fmt.Errorf("库 %q 引用了调用方未声明的类别 %q", s.Func, c)
		}
		bitMap[c] = b
	}
	argRefs := make([]Ref, 0, len(s.Params))
	for _, p := range s.Params {
		ref, ok := call.Args[p]
		if !ok {
			return nil, fmt.Errorf("库 %q 的参数 %q 缺少实参绑定", s.Func, p)
		}
		argRefs = append(argRefs, ref)
	}
	retRefs := make([]Ref, 0, len(call.RetRefs))
	for _, ref := range call.RetRefs {
		if ref.Var != "" {
			retRefs = append(retRefs, ref)
		}
	}

	eff := &callEffect{
		guaranteed: make(map[Ref]uint64),
		perRet:     make(map[RetVal]map[Ref]uint64),
		argRefs:    argRefs,
		retRefs:    retRefs,
	}
	rvs := make([]RetVal, 0, len(s.Returns))
	for rv := range s.Returns {
		rvs = append(rvs, rv)
	}
	// 固定返回值顺序，保证交集计算可复现。
	sortRetVals(rvs)
	eff.rv = rvs
	for i, rv := range rvs {
		rs := s.Returns[rv]
		slots := make(map[Ref]uint64)
		for p, set := range rs.ParamClean {
			ref := call.Args[p]
			slots[ref] |= translateBits(set, s.Categories, bitMap)
		}
		for _, set := range rs.ResultClean {
			for _, ref := range retRefs {
				slots[ref] |= translateBits(set, s.Categories, bitMap)
			}
		}
		for fld, set := range rs.FieldClean {
			base, ok := call.Args[fld.Var]
			if !ok {
				continue
			}
			slots[Ref{Var: base.Var, Field: fld.Field}] |= translateBits(set, s.Categories, bitMap)
		}
		eff.perRet[rv] = slots
		if i == 0 {
			for ref, bits := range slots {
				eff.guaranteed[ref] = bits
			}
		} else {
			for ref, bits := range eff.guaranteed {
				// 缺失槽位按“无净化（0）”参与交集，条件保证不得提升为无条件保证。
				eff.guaranteed[ref] = bits & slots[ref]
			}
		}
	}
	return eff, nil
}

func translateBits(set CatSet, srcOrder []Category, bitMap map[Category]uint64) uint64 {
	var out uint64
	for _, c := range set.List(srcOrder) {
		out |= bitMap[c]
	}
	return out
}

func sortRetVals(rvs []RetVal) {
	for i := 1; i < len(rvs); i++ {
		for j := i; j > 0 && rvs[j-1] > rvs[j]; j-- {
			rvs[j-1], rvs[j] = rvs[j], rvs[j-1]
		}
	}
}
