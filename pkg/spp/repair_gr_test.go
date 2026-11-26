package spp

import "testing"

// TestBackupLinkViolation 验收 4/5：违规备份链路打破无谷单调性，
// 触发争议轮与多稳态；最小修复后恢复唯一稳态。
func TestBackupLinkViolation(t *testing.T) {
	report, g := analyzeFixture(t, "backup_violation.json")

	if len(report.GR.ValleyFreeViolations) < 2 {
		t.Fatalf("两条备份路径都应被无谷核验拦截, 实际 %d 条", len(report.GR.ValleyFreeViolations))
	}
	if report.GR.StrictlyValleyFree() {
		t.Fatal("违规备份链路下严格无谷核验必须失败")
	}
	if !report.HasDisputeWheel {
		t.Fatal("备份链路引发的偏好循环必须形成争议轮")
	}
	wheel := report.Wheels[0]
	wantPivots := map[int]bool{2: true, 3: true}
	if len(wheel.Pivots) != 2 || !wantPivots[wheel.Pivots[0]] || !wantPivots[wheel.Pivots[1]] {
		t.Fatalf("期望 pivot 环 (2,3), 得到 %v", wheel.Pivots)
	}
	verifyWheelProof(t, g, wheel)
	if report.Outcome != OutcomeMultiple {
		t.Fatalf("备份链路实例应为多稳态, 得到 %s", report.Outcome)
	}

	if report.Repair == nil {
		t.Fatal("必须给出最小修复方案")
	}
	if len(report.Repair.Actions) != 1 {
		t.Fatalf("最小修复应为 1 个动作, 实际 %d 个", len(report.Repair.Actions))
	}
	if !report.Repair.UniqueStable || len(report.Repair.StableAssignments) != 1 {
		t.Fatal("修复后必须恰好存在一个稳态")
	}

	// 要求恢复严格无谷：禁用整条备份对等链路（两个方向）后同样唯一稳态。
	cfg := loadFixture(t, "backup_violation.json")
	grOnly, err := Analyze(cfg, AnalysisOptions{
		WheelLimit: DefaultWheelLimit,
		Repair:     RepairOptions{MaxSize: 2, MaxWheelLimit: DefaultWheelLimit, OnlyValleyFree: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	if grOnly.Repair == nil || !grOnly.Repair.RestoresValleyFree || !grOnly.Repair.UniqueStable {
		t.Fatal("严格无谷修复必须恢复 GR 合规并得到唯一稳态")
	}
}

// TestRepairActionTypes 验证两类最小修复手段（偏好排名修改、非核心导出边
// 禁用）都可被搜索到，且类型化动作字段完整。
func TestRepairActionTypes(t *testing.T) {
	cfg := loadFixture(t, "disagree.json")
	g, err := Load(cfg)
	if err != nil {
		t.Fatal(err)
	}
	gr := analyzeGR(g)
	actions := candidateActions(g, gr)

	var sawPref, sawEdge bool
	for _, a := range actions {
		switch a.(type) {
		case PreferenceEdit:
			sawPref = true
		case ExportEdgeDisable:
			sawEdge = true
		}
		ta := toTyped(a)
		if ta.Kind == "" {
			t.Fatal("类型化动作必须带 kind")
		}
	}
	if !sawPref || !sawEdge {
		t.Fatalf("候选动作必须同时包含偏好修改与导出边禁用, pref=%v edge=%v", sawPref, sawEdge)
	}

	// 强制只允许偏好修改：过滤掉所有导出边动作后仍应得到唯一稳态修复。
	var prefOnly []Action
	for _, a := range actions {
		if a.Kind() == "preference_edit" {
			prefOnly = append(prefOnly, a)
		}
	}
	plan := searchCombinations(g, cfg, prefOnly, 1, RepairOptions{MaxWheelLimit: DefaultWheelLimit})
	if plan == nil || !plan.UniqueStable {
		t.Fatal("单条偏好排名修改必须能消除 Disagree 争议轮并得到唯一稳态")
	}
	if _, ok := plan.Actions[0].(PreferenceEdit); !ok {
		t.Fatal("偏好修复方案的动作类型应为 PreferenceEdit")
	}
	if plan.TypedActions[0].Path == nil {
		t.Fatal("偏好修改的类型化动作必须携带被禁用的路径")
	}
}

// TestExportFilterExpansion 核验无谷展开等价于 Gao-Rexford 导出过滤：
// 来自对等体的路由不得向另一个对等体或供应商转运。
func TestExportFilterExpansion(t *testing.T) {
	cfg := loadFixture(t, "export_filter.json")
	g, err := Load(cfg)
	if err != nil {
		t.Fatal(err)
	}
	report := analyzeGR(g)
	if !report.StrictlyValleyFree() {
		t.Fatalf("正向夹具应严格无谷: %+v", report.ValleyFreeViolations)
	}
	legal3 := g.ValleyFreeLegalPaths(3)
	if len(legal3) != 2 {
		t.Fatalf("AS3 只应有两条客户上行合法路径, 得到 %d: %v", len(legal3), legal3)
	}
	illegal := Path{3, 1, 2, 0}
	for _, p := range legal3 {
		if p.Equal(illegal) {
			t.Fatal("含谷路径 3->1->2->0 不应出现在无谷展开结果中")
		}
	}
	if reason := g.valleyFreeReason(illegal); reason == "" {
		t.Fatal("违规路径必须给出可读原因")
	}

	// 商业环检测：制造一个 c2p 环并确认被发现。
	cyclic := &Config{
		Destination: 0,
		Nodes:       []int{0, 1, 2},
		Edges: []EdgeConfig{
			{From: 1, To: 2, Kind: "c2p"},
			{From: 2, To: 1, Kind: "c2p"},
			{From: 1, To: 0, Kind: "c2p"},
		},
		Preferences: map[string][]Path{"0": {{0}}, "1": {{1, 0}}, "2": {{2, 1, 0}}},
	}
	cg, err := Load(cyclic)
	if err != nil {
		t.Fatal(err)
	}
	cycles := cg.FindCommercialCycles()
	if len(cycles) != 1 {
		t.Fatalf("期望检出 1 个客户-供应商商业环, 得到 %d", len(cycles))
	}
}

// TestSolverSoundness 用穷举全部分配的方式交叉验证求解器：
// 求解器结果必须与独立暴力枚举完全一致，且每个返回分配都通过最终核验。
func TestSolverSoundness(t *testing.T) {
	for _, name := range []string{"bad_gadget.json", "disagree.json", "clean_valley_free.json", "backup_violation.json"} {
		cfg := loadFixture(t, name)
		g, err := Load(cfg)
		if err != nil {
			t.Fatal(err)
		}
		result := g.Solve(Options{})
		brute := bruteForceStable(g)
		if len(result.Assignments) != len(brute) {
			t.Fatalf("%s: 求解器 %d 个稳态 vs 暴力枚举 %d 个", name, len(result.Assignments), len(brute))
		}
		for _, a := range result.Assignments {
			if !g.fullyStable(a) {
				t.Fatalf("%s: 求解器返回了不稳定分配 %v", name, a)
			}
			if !containsAssignment(brute, a) {
				t.Fatalf("%s: 求解器分配不在暴力枚举集合中", name)
			}
		}
	}
}

func bruteForceStable(g *Graph) []Assignment {
	nodes := g.SortedNodes()
	var domains [][]Path
	for _, as := range nodes {
		if as == g.Destination {
			domains = append(domains, []Path{{as}})
			continue
		}
		domains = append(domains, append(g.Preferences(as), nil))
	}
	var stable []Assignment
	current := make(Assignment)
	var rec func(idx int)
	rec = func(idx int) {
		if idx == len(nodes) {
			if g.fullyStable(current) {
				cp := make(Assignment, len(current))
				for k, v := range current {
					cp[k] = copyPath(v)
				}
				stable = append(stable, cp)
			}
			return
		}
		as := nodes[idx]
		for _, choice := range domains[idx] {
			current[as] = choice
			rec(idx + 1)
		}
		delete(current, as)
	}
	rec(0)
	return stable
}
