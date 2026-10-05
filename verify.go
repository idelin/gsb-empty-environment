package main

import "sort"

// DepCheck 记录一条依赖边的核验结果。
type DepCheck struct {
	Instance string `json:"instance"`
	Pred     string `json:"predecessor"`
	PredEnd  int    `json:"pred_end"`
	Start    int    `json:"start"`
	OK       bool   `json:"ok"`
}

// ResourceViolation 记录一次资源池超用。
type ResourceViolation struct {
	Resource string `json:"resource"`
	From     int    `json:"from"`
	To       int    `json:"to"`
	Used     int    `json:"used"`
	Capacity int    `json:"capacity"`
}

// Verification 是对输出计划的独立核验报告。
type Verification struct {
	InstanceCount  int                 `json:"instance_count"`
	MaxInstances   int                 `json:"max_instances"`
	IdentityOK     bool                `json:"identity_ok"`
	IdentityIssues []string            `json:"identity_issues,omitempty"`
	DependenciesOK bool                `json:"dependencies_ok"`
	DepChecks      []DepCheck          `json:"dependency_checks"`
	ResourcesOK    bool                `json:"resources_ok"`
	Violations     []ResourceViolation `json:"resource_violations,omitempty"`
}

// verify 独立复核计划：实例身份唯一且返工未覆盖原工序、
// 每条依赖边满足 前驱完工 <= 后继开工、每个资源池在任意时刻不超用。
func verify(insts []Instance, resNames []string, caps map[string]int, starts []int) Verification {
	v := Verification{
		InstanceCount:  len(insts),
		MaxInstances:   MaxInstances,
		IdentityOK:     true,
		DependenciesOK: true,
		ResourcesOK:    true,
	}

	// 身份核验：实例 ID 全局唯一；每个失败工序的原实例与其返工实例并存。
	seen := make(map[string]bool, len(insts))
	originHasRework := make(map[string]bool)
	for _, inst := range insts {
		if seen[inst.ID] {
			v.IdentityOK = false
			v.IdentityIssues = append(v.IdentityIssues, "实例 id 重复: "+inst.ID)
		}
		seen[inst.ID] = true
		if inst.IsRework {
			originHasRework[inst.Origin] = true
		}
	}
	for _, inst := range insts {
		if inst.IsRework && !seen[inst.Origin] {
			v.IdentityOK = false
			v.IdentityIssues = append(v.IdentityIssues,
				"返工实例 "+inst.ID+" 的来源工序 "+inst.Origin+" 缺失（被覆盖？）")
		}
	}
	if len(insts) > MaxInstances {
		v.IdentityOK = false
		v.IdentityIssues = append(v.IdentityIssues, "实例数超过上限")
	}

	// 依赖核验。
	idx := make(map[string]int, len(insts))
	for i, inst := range insts {
		idx[inst.ID] = i
	}
	for i, inst := range insts {
		for _, p := range inst.Preds {
			j := idx[p]
			chk := DepCheck{
				Instance: inst.ID,
				Pred:     p,
				PredEnd:  starts[j] + insts[j].Dur,
				Start:    starts[i],
			}
			chk.OK = chk.PredEnd <= chk.Start
			if !chk.OK {
				v.DependenciesOK = false
			}
			v.DepChecks = append(v.DepChecks, chk)
		}
	}
	sort.Slice(v.DepChecks, func(a, b int) bool {
		if v.DepChecks[a].Instance != v.DepChecks[b].Instance {
			return v.DepChecks[a].Instance < v.DepChecks[b].Instance
		}
		return v.DepChecks[a].Pred < v.DepChecks[b].Pred
	})

	// 资源核验：扫描线检查每个资源池在任意时刻的占用。
	for _, res := range resNames {
		type ev struct {
			t     int
			delta int
		}
		var evs []ev
		for i, inst := range insts {
			d := inst.Demands[res]
			if d == 0 {
				continue
			}
			evs = append(evs, ev{starts[i], d}, ev{starts[i] + inst.Dur, -d})
		}
		sort.Slice(evs, func(a, b int) bool {
			if evs[a].t != evs[b].t {
				return evs[a].t < evs[b].t
			}
			return evs[a].delta < evs[b].delta // 同一时刻先释放后占用
		})
		used, prev := 0, 0
		for _, e := range evs {
			if e.t > prev && used > caps[res] {
				v.ResourcesOK = false
				v.Violations = append(v.Violations, ResourceViolation{
					Resource: res, From: prev, To: e.t, Used: used, Capacity: caps[res],
				})
			}
			used += e.delta
			prev = e.t
		}
	}
	return v
}
