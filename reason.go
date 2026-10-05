package main

import "fmt"

// 删除原因类别。
const (
	ReasonDanglingFK  = "dangling_foreign_key"  // 引用的父行在输入中根本不存在
	ReasonFKCascade   = "foreign_key_cascade"   // 父行被删除，被迫级联删除
	ReasonUniqueKey   = "unique_key_violation"  // 与被保留行在同一唯一键上取值相同
	ReasonRowConflict = "declared_row_conflict" // 与被保留行存在用户指定的行间冲突
	ReasonTradeoff    = "weight_tradeoff"       // 全局权重最优下的主动取舍
)

// DeletionReason 描述一行的删除原因；级联删除通过 CausedBy 串成闭包，
// Chain 给出从根本原因到该行的完整传导链。
type DeletionReason struct {
	Table         string   `json:"table"`
	Row           string   `json:"row"`
	Weight        int64    `json:"weight"`
	Kind          string   `json:"kind"`
	Detail        string   `json:"detail"`
	CausedBy      *RowRef  `json:"caused_by,omitempty"`
	ConflictsWith *RowRef  `json:"conflicts_with,omitempty"`
	Chain         []RowRef `json:"chain"`
}

// ExplainDeletions 为最优解中的每个被删行计算原因，并沿外键级联边求传递闭包。
func ExplainDeletions(m *model, sol solution) []DeletionReason {
	n := len(m.rowRef)
	kept := sol.keptMask
	deleted := func(i int) bool { return kept&(1<<uint(i)) == 0 }

	reasons := make([]DeletionReason, n)
	isDeleted := make([]bool, n)
	for i := 0; i < n; i++ {
		isDeleted[i] = deleted(i)
	}

	for i := 0; i < n; i++ {
		if !isDeleted[i] {
			continue
		}
		ref := m.rowRef[i]
		dr := DeletionReason{Table: ref.Table, Row: ref.Row, Weight: m.weight[i]}

		switch {
		case m.dangling[i]:
			dr.Kind = ReasonDanglingFK
			for _, req := range m.reqs {
				if req.child == i && len(req.candidates) == 0 {
					dr.Detail = fmt.Sprintf("外键 %s 取值 %s 在父表 %s 中不存在，只能删除",
						req.fk, req.childValue, req.fk.ParentTable)
					break
				}
			}
		default:
			if parent, req, ok := cascadeParent(m, i, isDeleted); ok {
				pref := m.rowRef[parent]
				dr.Kind = ReasonFKCascade
				dr.CausedBy = &pref
				dr.Detail = fmt.Sprintf("外键 %s 取值 %s 的全部父行均被删除（如 %s），被迫级联删除",
					req.fk, req.childValue, pref)
			} else if other, g, ok := uniqueConflict(m, i, kept); ok {
				oref := m.rowRef[other]
				dr.Kind = ReasonUniqueKey
				dr.ConflictsWith = &oref
				dr.Detail = fmt.Sprintf("与保留行 %s 在唯一键 %s(%v) 上取值相同，按权重取舍删除本行",
					oref, g.table, g.columns)
			} else if other, text, ok := declaredConflict(m, i, kept); ok {
				oref := m.rowRef[other]
				dr.Kind = ReasonRowConflict
				dr.ConflictsWith = &oref
				dr.Detail = fmt.Sprintf("与保留行 %s 存在指定行间冲突（%s），按权重取舍删除本行", oref, text)
			} else {
				dr.Kind = ReasonTradeoff
				dr.Detail = "无任何单行约束直接强制删除；属于全局删除权重最小化下的联合取舍"
			}
		}
		reasons[i] = dr
	}

	// 传递闭包：沿 caused_by 追到根本原因，形成完整传导链。
	var chain func(i int, seen map[int]bool) []RowRef
	chain = func(i int, seen map[int]bool) []RowRef {
		if seen[i] {
			return nil // 防御循环外键
		}
		seen[i] = true
		r := reasons[i]
		if r.CausedBy == nil {
			return []RowRef{m.rowRef[i]}
		}
		p := m.index[*r.CausedBy]
		return append(chain(p, seen), m.rowRef[i])
	}

	var out []DeletionReason
	for i := 0; i < n; i++ {
		if !isDeleted[i] {
			continue
		}
		r := reasons[i]
		r.Chain = chain(i, make(map[int]bool))
		out = append(out, r)
	}
	return out
}

// cascadeParent 判断行 i 是否因某条外键的全部候选父行被删而被迫删除。
func cascadeParent(m *model, i int, isDeleted []bool) (int, fkReq, bool) {
	for _, req := range m.reqs {
		if req.child != i || len(req.candidates) == 0 {
			continue
		}
		allGone := true
		for _, c := range req.candidates {
			if !isDeleted[c] {
				allGone = false
				break
			}
		}
		if allGone {
			return req.candidates[0], req, true
		}
	}
	return 0, fkReq{}, false
}

// uniqueConflict 找到与行 i 同唯一键组且被保留的行。
func uniqueConflict(m *model, i int, kept uint32) (int, ukGroup, bool) {
	for _, g := range m.groups {
		inGroup := false
		for _, mem := range g.members {
			if mem == i {
				inGroup = true
				break
			}
		}
		if !inGroup {
			continue
		}
		for _, mem := range g.members {
			if mem != i && kept&(1<<uint(mem)) != 0 {
				return mem, g, true
			}
		}
	}
	return 0, ukGroup{}, false
}

// declaredConflict 找到与行 i 存在指定冲突且被保留的行。
func declaredConflict(m *model, i int, kept uint32) (int, string, bool) {
	for _, p := range m.conflictPairs {
		var other int
		if p[0] == i {
			other = p[1]
		} else if p[1] == i {
			other = p[0]
		} else {
			continue
		}
		if kept&(1<<uint(other)) != 0 {
			return other, m.conflictText[p], true
		}
	}
	return 0, "", false
}
