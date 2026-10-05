package main

import (
	"fmt"
	"strings"
)

// Verify 对保留集重新检查全部约束，返回违规列表（应为空）。
func Verify(m *Model, state []int) []string {
	var violations []string

	for _, g := range m.Groups {
		var keptRefs []string
		for _, mi := range g.Members {
			if state[mi] == kept {
				keptRefs = append(keptRefs, m.Rows[mi].Ref())
			}
		}
		if len(keptRefs) > 1 {
			violations = append(violations, fmt.Sprintf("唯一键 %s(%s) 取值 %s 仍保留多行: %s",
				g.Table, strings.Join(g.Key, "+"), g.Value, strings.Join(keptRefs, ", ")))
		}
	}

	for _, c := range m.Conflicts {
		var keptRefs []string
		for _, mi := range c.Rows {
			if state[mi] == kept {
				keptRefs = append(keptRefs, m.Rows[mi].Ref())
			}
		}
		if len(keptRefs) > 1 {
			violations = append(violations, fmt.Sprintf("冲突规则 %q 仍保留多行: %s",
				c.Name, strings.Join(keptRefs, ", ")))
		}
	}

	for fi, fk := range m.FKs {
		for child, cands := range m.fkCandidates[fi] {
			if cands == nil || state[child] != kept {
				continue
			}
			ok := false
			for _, p := range cands {
				if state[p] == kept {
					ok = true
					break
				}
			}
			if !ok {
				violations = append(violations, fmt.Sprintf("外键 %s: 保留行 %s 没有保留的父行",
					fk.DisplayName(), m.Rows[child].Ref()))
			}
		}
	}
	return violations
}
