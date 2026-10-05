package main

import "testing"

func solveFromInput(t *testing.T, in *Input) ([]Instance, []int, int) {
	t.Helper()
	insts, resNames, caps, err := expand(in)
	if err != nil {
		t.Fatalf("expand 失败: %v", err)
	}
	s := newSolver(insts, resNames, caps)
	starts, makespan := s.solve()
	v := verify(insts, resNames, caps, starts)
	if !v.DependenciesOK || !v.ResourcesOK || !v.IdentityOK {
		t.Fatalf("计划未通过核验: %+v", v)
	}
	return insts, starts, makespan
}

func TestPureChain(t *testing.T) {
	in := &Input{
		Resources: map[string]int{"m": 4},
		Jobs: []JobInput{
			{ID: "a", Dur: 2, Demands: map[string]int{"m": 1}},
			{ID: "b", Dur: 3, Demands: map[string]int{"m": 1}, Deps: []string{"a"}},
			{ID: "c", Dur: 1, Demands: map[string]int{"m": 1}, Deps: []string{"b"}},
		},
	}
	_, _, mk := solveFromInput(t, in)
	if mk != 6 {
		t.Fatalf("纯链完工时间应为 6，得到 %d", mk)
	}
}

func TestResourceSerialization(t *testing.T) {
	in := &Input{
		Resources: map[string]int{"m": 1},
		Jobs: []JobInput{
			{ID: "a", Dur: 2, Demands: map[string]int{"m": 1}},
			{ID: "b", Dur: 2, Demands: map[string]int{"m": 1}},
		},
	}
	_, _, mk := solveFromInput(t, in)
	if mk != 4 {
		t.Fatalf("单资源下两工序应串行，完工时间应为 4，得到 %d", mk)
	}
}

func TestReworkExpansionAndIdentity(t *testing.T) {
	in := &Input{
		Resources: map[string]int{"m": 2},
		Jobs: []JobInput{
			{ID: "a", Dur: 1, Demands: map[string]int{"m": 1}},
			{ID: "b", Dur: 1, Demands: map[string]int{"m": 1}, Deps: []string{"a"}},
		},
		Failures: []FailureInput{
			{Job: "a", Rework: []ReworkStep{{ID: "fix", Dur: 2, Demands: map[string]int{"m": 1}}}},
		},
	}
	insts, starts, mk := solveFromInput(t, in)
	if mk != 4 { // a(1) + a#fix(2) + b(1)
		t.Fatalf("含返工链的完工时间应为 4，得到 %d", mk)
	}
	idx := make(map[string]int)
	for i, inst := range insts {
		idx[inst.ID] = i
	}
	if _, ok := idx["a"]; !ok {
		t.Fatal("原工序 a 的身份丢失")
	}
	rw, ok := idx["a#fix"]
	if !ok {
		t.Fatal("返工实例 a#fix 缺失")
	}
	if !insts[rw].IsRework || insts[rw].Origin != "a" {
		t.Fatalf("返工实例身份错误: %+v", insts[rw])
	}
	// b 必须挂在返工链末端之后。
	if starts[idx["b"]] < starts[rw]+insts[rw].Dur {
		t.Fatal("后继工序未排在返工链末端之后")
	}
}

func TestReworkSharesResourcePool(t *testing.T) {
	in := &Input{
		Resources: map[string]int{"m": 1},
		Jobs: []JobInput{
			{ID: "x", Dur: 2, Demands: map[string]int{"m": 1}},
			{ID: "y", Dur: 3, Demands: map[string]int{"m": 1}},
		},
		Failures: []FailureInput{
			{Job: "x", Rework: []ReworkStep{{ID: "rx", Dur: 2, Demands: map[string]int{"m": 1}}}},
		},
	}
	_, _, mk := solveFromInput(t, in)
	if mk != 7 { // 单台机器上 x + x#rx + y 全部串行
		t.Fatalf("返工与原工序共用资源池，完工时间应为 7，得到 %d", mk)
	}
}

func TestOptimalFragmentedCapacity(t *testing.T) {
	// a 独占整机造成容量碎片化：总工作量下界为 6，但最优实为 7。
	in := &Input{
		Resources: map[string]int{"m": 2},
		Jobs: []JobInput{
			{ID: "a", Dur: 3, Demands: map[string]int{"m": 2}},
			{ID: "b", Dur: 2, Demands: map[string]int{"m": 1}},
			{ID: "c", Dur: 2, Demands: map[string]int{"m": 1}},
			{ID: "d", Dur: 2, Demands: map[string]int{"m": 1}, Deps: []string{"b", "c"}},
		},
	}
	_, _, mk := solveFromInput(t, in)
	if mk != 7 { // 如 b、c 并行 [0,2)，a 独占 [2,5)，d 收尾 [5,7)
		t.Fatalf("最优完工时间应为 7，得到 %d", mk)
	}
}

func TestInstanceLimit(t *testing.T) {
	in := &Input{Resources: map[string]int{"m": 16}}
	for i := 0; i < 16; i++ {
		in.Jobs = append(in.Jobs, JobInput{
			ID: string(rune('a' + i)), Dur: 1, Demands: map[string]int{"m": 1},
		})
	}
	if _, _, _, err := expand(in); err == nil {
		t.Fatal("超过 15 个实例应当报错")
	}
}

func TestCycleDetected(t *testing.T) {
	in := &Input{
		Resources: map[string]int{"m": 1},
		Jobs: []JobInput{
			{ID: "a", Dur: 1, Demands: map[string]int{"m": 1}, Deps: []string{"b"}},
			{ID: "b", Dur: 1, Demands: map[string]int{"m": 1}, Deps: []string{"a"}},
		},
	}
	if _, _, _, err := expand(in); err == nil {
		t.Fatal("依赖环应当报错")
	}
}
