package main

import (
	"fmt"
	"strings"
)

// Reason 描述一行被删除的原因；级联删除时 Children 递归携带父行的删除原因，
// 形成完整的原因闭包。
type Reason struct {
	Kind     string    `json:"kind"` // unique_key | conflict | dangling_fk | cascade | parent_deleted | min_weight
	Message  string    `json:"message"`
	Children []*Reason `json:"children,omitempty"`
}

// ComputeReasons 基于最优解的保留/删除状态，为每个被删除行推导原因闭包。
// 最优性保证：任何被删除的行若没有任何约束层面的原因，则把它加回保留集
// 会得到更优的可行解，矛盾——因此每个删除行至少有一个原因。
func ComputeReasons(m *Model, state []int) map[int][]*Reason {
	memo := make(map[int][]*Reason, len(m.Rows))
	visiting := make([]bool, len(m.Rows))

	var build func(int) []*Reason
	build = func(i int) []*Reason {
		if rs, ok := memo[i]; ok {
			return rs
		}
		if visiting[i] {
			return nil // 防御环状级联引用
		}
		visiting[i] = true
		var out []*Reason

		// 唯一键：与同取值的保留行冲突
		for _, g := range m.groupsOf[i] {
			for _, mi := range g.Members {
				if mi != i && state[mi] == kept {
					out = append(out, &Reason{
						Kind: "unique_key",
						Message: fmt.Sprintf("唯一键 %s(%s) 取值 %s 与保留行 %s 重复",
							g.Table, strings.Join(g.Key, "+"), g.Value, m.Rows[mi].Ref()),
					})
				}
			}
		}

		// 行间冲突：与保留行互斥
		for _, ci := range m.conflictsOf[i] {
			c := m.Conflicts[ci]
			for _, mi := range c.Rows {
				if mi != i && state[mi] == kept {
					out = append(out, &Reason{
						Kind:    "conflict",
						Message: fmt.Sprintf("冲突规则 %q 与保留行 %s 互斥", c.Name, m.Rows[mi].Ref()),
					})
				}
			}
		}

		// 外键：悬空引用或父行被删导致的级联
		for fi, fk := range m.FKs {
			cands := m.fkCandidates[fi][i]
			if cands == nil {
				continue
			}
			if len(cands) == 0 {
				out = append(out, &Reason{
					Kind: "dangling_fk",
					Message: fmt.Sprintf("外键 %s 在父表中无匹配行（悬空引用）",
						fk.DisplayName()),
				})
				continue
			}
			anyKept := false
			for _, p := range cands {
				if state[p] == kept {
					anyKept = true
					break
				}
			}
			if !anyKept {
				r := &Reason{
					Kind: "cascade",
					Message: fmt.Sprintf("外键 %s 的全部候选父行均被删除，级联删除",
						fk.DisplayName()),
				}
				for _, p := range cands {
					r.Children = append(r.Children, &Reason{
						Kind:     "parent_deleted",
						Message:  fmt.Sprintf("父行 %s 的删除原因：", m.Rows[p].Ref()),
						Children: build(p),
					})
				}
				out = append(out, r)
			}
		}

		if len(out) == 0 {
			out = append(out, &Reason{
				Kind:    "min_weight",
				Message: "为最小化总删除权重而删除（无直接约束违反）",
			})
		}
		visiting[i] = false
		memo[i] = out
		return out
	}

	res := make(map[int][]*Reason)
	for i := range m.Rows {
		if state[i] == deleted {
			res[i] = build(i)
		}
	}
	return res
}
