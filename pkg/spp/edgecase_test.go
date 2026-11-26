package spp

import "testing"

// TestDisconnectedNode 验证没有任何允许路径的 AS 以空选择进入唯一稳态。
func TestDisconnectedNode(t *testing.T) {
	cfg := &Config{
		Destination: 0,
		Nodes:       []int{0, 1, 2},
		Edges: []EdgeConfig{
			{From: 1, To: 0, Kind: "c2p"},
		},
		Preferences: map[string][]Path{
			"0": {{0}},
			"1": {{1, 0}},
			// AS2 没有任何到源站的路径
		},
	}
	g, err := Load(cfg)
	if err != nil {
		t.Fatal(err)
	}
	res := g.Solve(Options{})
	if len(res.Assignments) != 1 {
		t.Fatalf("孤立 AS 应得到唯一稳态, 实际 %d 个", len(res.Assignments))
	}
	a := res.Assignments[0]
	if a[2] != nil {
		t.Fatalf("孤立 AS2 必须选空, 得到 %v", a[2])
	}
	if !g.fullyStable(a) {
		t.Fatal("含空选择的分配必须通过最终稳定性核验")
	}
}

// TestMaxAssignmentLimit 验证枚举上限与截断标记。
func TestMaxAssignmentLimit(t *testing.T) {
	cfg := loadFixture(t, "disagree.json")
	g, err := Load(cfg)
	if err != nil {
		t.Fatal(err)
	}
	full := g.Solve(Options{})
	if len(full.Assignments) < 2 {
		t.Fatalf("Disagree 至少两个稳态, 实际 %d", len(full.Assignments))
	}
	capped := g.Solve(Options{MaxAssignments: 1})
	if len(capped.Assignments) != 1 || !capped.Truncated {
		t.Fatal("命中 MaxAssignments 时应只返回 1 个并标记截断")
	}
}
