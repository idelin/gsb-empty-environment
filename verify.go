package main

import "fmt"

// CheckResult 是一类约束的核验结果。
type CheckResult struct {
	Name       string   `json:"name"`
	Violations []string `json:"violations"`
	OK         bool     `json:"ok"`
}

// Verification 是对保留表的全量复核：唯一键、外键、指定冲突逐项重查。
type Verification struct {
	UniqueKeys  []CheckResult `json:"unique_keys"`
	ForeignKeys []CheckResult `json:"foreign_keys"`
	Conflicts   CheckResult   `json:"declared_conflicts"`
	OK          bool          `json:"ok"`
}

// VerifyKept 独立于我求解器重新检查保留集合，作为结果的核验闭环。
func VerifyKept(m *model, sol solution) Verification {
	kept := sol.keptMask
	isKept := func(i int) bool { return kept&(1<<uint(i)) != 0 }
	v := Verification{OK: true}

	// 唯一键：同组保留行数不得超过 1。
	for _, g := range m.groups {
		cr := CheckResult{Name: fmt.Sprintf("%s UNIQUE%v", g.table, g.columns), OK: true}
		var keptRefs []string
		for _, mem := range g.members {
			if isKept(mem) {
				keptRefs = append(keptRefs, m.rowRef[mem].String())
			}
		}
		if len(keptRefs) > 1 {
			cr.OK = false
			cr.Violations = append(cr.Violations,
				fmt.Sprintf("唯一键冲突：%v 同时保留", keptRefs))
		}
		v.UniqueKeys = append(v.UniqueKeys, cr)
		if !cr.OK {
			v.OK = false
		}
	}

	// 外键：每个保留的子行至少有一个保留的父行；悬空行不得保留。
	for _, req := range m.reqs {
		name := req.fk.String() + " child=" + m.rowRef[req.child].String()
		cr := CheckResult{Name: name, OK: true}
		if isKept(req.child) {
			if len(req.candidates) == 0 {
				cr.OK = false
				cr.Violations = append(cr.Violations,
					fmt.Sprintf("悬空引用 %s 仍被保留", req.childValue))
			} else {
				hasParent := false
				for _, c := range req.candidates {
					if isKept(c) {
						hasParent = true
						break
					}
				}
				if !hasParent {
					cr.OK = false
					cr.Violations = append(cr.Violations,
						fmt.Sprintf("父行全部被删除，子行 %s 仍保留", m.rowRef[req.child]))
				}
			}
		}
		v.ForeignKeys = append(v.ForeignKeys, cr)
		if !cr.OK {
			v.OK = false
		}
	}

	// 指定行间冲突：两端不得同时保留。
	v.Conflicts = CheckResult{Name: "declared_conflicts", OK: true}
	for _, p := range m.conflictPairs {
		if isKept(p[0]) && isKept(p[1]) {
			v.Conflicts.OK = false
			v.Conflicts.Violations = append(v.Conflicts.Violations,
				fmt.Sprintf("冲突双方同时保留: %s 与 %s", m.rowRef[p[0]], m.rowRef[p[1]]))
		}
	}
	if !v.Conflicts.OK {
		v.OK = false
	}
	return v
}
