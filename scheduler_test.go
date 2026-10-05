package main

import "testing"

func mustInstance(t *testing.T, p Problem) []*Activity {
	acts, err := p.Instance()
	if err != nil {
		t.Fatalf("Instance() 报错: %v", err)
	}
	return acts
}

func entryMap(sol Solution) map[string]ScheduleEntry {
	m := map[string]ScheduleEntry{}
	for _, e := range sol.Entries {
		m[e.ID] = e
	}
	return m
}

// 失败工序触发返工链：A -> A-repair -> A-recheck -> B，单资源容量 1 强制串行。
func TestReworkChainExpansionAndSchedule(t *testing.T) {
	p := Problem{
		Resources: ResourceMap{"machine": 1},
		Tasks: []TaskSpec{
			{ID: "A", Duration: 3, Resources: ResourceMap{"machine": 1}, Inspection: "fail",
				Rework: []ReworkSpec{
					{ID: "A-repair", Duration: 2, Resources: ResourceMap{"machine": 1}},
					{ID: "A-recheck", Duration: 1, Resources: ResourceMap{"machine": 1}},
				}},
			{ID: "B", Duration: 4, Resources: ResourceMap{"machine": 1}, Predecessors: []string{"A"}},
		},
	}
	acts := mustInstance(t, p)
	if len(acts) != 4 {
		t.Fatalf("期望 4 个实例，得到 %d", len(acts))
	}
	sol, err := SolveOptimal(acts, p.Resources)
	if err != nil {
		t.Fatalf("SolveOptimal 报错: %v", err)
	}
	if !sol.Optimal {
		t.Fatal("期望已证明最优")
	}
	if sol.Makespan != 10 {
		t.Fatalf("单资源串行 makespan 应为 3+2+1+4=10，得到 %d", sol.Makespan)
	}
	m := entryMap(sol)
	// 身份保留：返工实例与原工序并存，且 B 接在返工链末端之后。
	if !(m["A"].Finish <= m["A-repair"].Start &&
		m["A-repair"].Finish <= m["A-recheck"].Start &&
		m["A-recheck"].Finish <= m["B"].Start) {
		t.Fatalf("返工链顺序错误: %+v", m)
	}
	if !m["A-repair"].IsRework || m["A"].IsRework {
		t.Fatal("返工身份标记错误")
	}
	rep, err := BuildReport(acts, p.Resources, sol)
	if err != nil {
		t.Fatalf("BuildReport 报错: %v", err)
	}
	if !rep.DepsOK || !rep.ResourceOK {
		t.Fatalf("核验应全部通过: deps=%v res=%v", rep.DepsOK, rep.ResourceOK)
	}
	// B 的依赖核验应指向返工链末端而非原工序 A。
	found := false
	for _, c := range rep.DepChecks {
		if c.Successor == "B" {
			found = true
			if c.Predecessor != "A-recheck" {
				t.Fatalf("B 的前驱应为 A-recheck，得到 %s", c.Predecessor)
			}
		}
	}
	if !found {
		t.Fatal("缺少 B 的依赖核验记录")
	}
}

// 检验通过：不插入返工；资源池共享使两条并行工序串行化。
func TestSharedResourcePoolSerializes(t *testing.T) {
	p := Problem{
		Resources: ResourceMap{"welder": 1},
		Tasks: []TaskSpec{
			{ID: "X", Duration: 5, Resources: ResourceMap{"welder": 1}, Inspection: "pass"},
			{ID: "Y", Duration: 5, Resources: ResourceMap{"welder": 1}},
		},
	}
	acts := mustInstance(t, p)
	sol, err := SolveOptimal(acts, p.Resources)
	if err != nil {
		t.Fatal(err)
	}
	if sol.Makespan != 10 {
		t.Fatalf("共享单资源应串行，makespan 应为 10，得到 %d", sol.Makespan)
	}
}

// 最优性关键场景：最优解要求刻意闲置，把资源留给即将解锁的长链工序。
func TestDeliberateIdleIsOptimal(t *testing.T) {
	p := Problem{
		Resources: ResourceMap{"crew": 2},
		Tasks: []TaskSpec{
			{ID: "A", Duration: 10, Resources: ResourceMap{"crew": 2}},
			{ID: "P", Duration: 1},
			{ID: "C", Duration: 1, Resources: ResourceMap{"crew": 2}, Predecessors: []string{"P"}},
			{ID: "T", Duration: 20, Predecessors: []string{"C"}},
		},
	}
	acts := mustInstance(t, p)
	sol, err := SolveOptimal(acts, p.Resources)
	if err != nil {
		t.Fatal(err)
	}
	// 若 A 在 0 开工，C 要等到 10，链尾到 31；最优是让 C 在 1 开工，A 等到 2。
	if sol.Makespan != 22 {
		t.Fatalf("期望 makespan 22（刻意闲置），得到 %d", sol.Makespan)
	}
	m := entryMap(sol)
	if m["A"].Start != 2 {
		t.Fatalf("A 应延迟到 t=2 开工，得到 t=%d", m["A"].Start)
	}
}

// 返工实例 ID 与已有工序冲突必须报错，防止覆盖原工序身份。
func TestReworkIDCollisionRejected(t *testing.T) {
	p := Problem{
		Resources: ResourceMap{"m": 1},
		Tasks: []TaskSpec{
			{ID: "A", Duration: 1, Resources: ResourceMap{"m": 1}, Inspection: "fail",
				Rework: []ReworkSpec{{ID: "B", Duration: 1, Resources: ResourceMap{"m": 1}}}},
			{ID: "B", Duration: 1, Resources: ResourceMap{"m": 1}},
		},
	}
	if _, err := p.Instance(); err == nil {
		t.Fatal("返工 ID 与现有工序冲突应报错")
	}
}

// 失败但无返工链、实例数超限、环，均应报错。
func TestValidation(t *testing.T) {
	failNoRework := Problem{
		Resources: ResourceMap{"m": 1},
		Tasks:     []TaskSpec{{ID: "A", Duration: 1, Inspection: "fail"}},
	}
	if _, err := failNoRework.Instance(); err == nil {
		t.Fatal("失败工序缺少返工链应报错")
	}

	tooMany := Problem{Resources: ResourceMap{"m": 1}}
	for i := 0; i < 16; i++ {
		tooMany.Tasks = append(tooMany.Tasks, TaskSpec{ID: string(rune('a' + i)), Duration: 1})
	}
	if _, err := tooMany.Instance(); err == nil {
		t.Fatal("超过 15 个实例应报错")
	}

	cyclic := Problem{
		Resources: ResourceMap{"m": 1},
		Tasks: []TaskSpec{
			{ID: "A", Duration: 1, Predecessors: []string{"B"}},
			{ID: "B", Duration: 1, Predecessors: []string{"A"}},
		},
	}
	if _, err := cyclic.Instance(); err == nil {
		t.Fatal("依赖环应报错")
	}
}

// 核验器必须能发现被篡改的排程（依赖违反）。
func TestVerifierCatchesViolation(t *testing.T) {
	p := Problem{
		Resources: ResourceMap{"m": 2},
		Tasks: []TaskSpec{
			{ID: "A", Duration: 3, Resources: ResourceMap{"m": 1}},
			{ID: "B", Duration: 2, Resources: ResourceMap{"m": 1}, Predecessors: []string{"A"}},
		},
	}
	acts := mustInstance(t, p)
	bad := Solution{Entries: []ScheduleEntry{
		{ID: "A", Start: 0, Finish: 3},
		{ID: "B", Start: 1, Finish: 3}, // 违反 A->B
	}, Makespan: 3}
	rep, err := BuildReport(acts, p.Resources, bad)
	if err != nil {
		t.Fatal(err)
	}
	if rep.DepsOK {
		t.Fatal("核验应发现依赖违反")
	}
}
