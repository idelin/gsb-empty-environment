package sensitivity

import (
	"strings"
	"testing"
)

func TestVerifyBoundedPlanNoViolation(t *testing.T) {
	events := &Source{ID: "e", Rel: "events", Cols: []string{"uid", "amount"}}
	users := &Source{ID: "u", Rel: "users", Cols: []string{"uid", "city"}}
	join := &Join{ID: "j", Left: events, Right: users,
		LeftKey: "uid", RightKey: "uid",
		LeftRepeat: NewRat(1), RightRepeat: NewRat(1)}
	sum := &TruncatedSum{ID: "s", In: join, Col: "amount",
		Lower: NewRat(0), Upper: NewRat(20)}

	report, err := Verify(sum, sampleTables(), map[string][]Row{
		"events": {{Values: []Cell{NewRat(2), NewRat(20)}}},
		"users":  {{Values: []Cell{NewRat(2), NewRat(7)}}},
	})
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if report.Violation {
		t.Fatalf("不应出现违规:\n%s", report.Render())
	}
	if report.CheckedNeighbors != 8 {
		t.Fatalf("枚举邻居数 = %d, want 8", report.CheckedNeighbors)
	}
	// 加入 amount=20 且能 1:1 连接上的行，经验差值恰好达到推导上界 20。
	assertRat(t, report.MaxScalarDelta, "20", "根节点经验最大差值")
	if len(report.BoundChecks) != 2 {
		t.Fatalf("重复上界检查数 = %d, want 2", len(report.BoundChecks))
	}
	for _, bc := range report.BoundChecks {
		if bc.Violation {
			t.Fatalf("重复上界检查不应违规: %s", bc.Message)
		}
	}
}

func TestVerifyJoinAmplificationMatchesBound(t *testing.T) {
	events := &Source{Rel: "events", Cols: []string{"uid", "amount"}}
	users := &Source{Rel: "users", Cols: []string{"uid", "city"}}
	join := &Join{Left: events, Right: users, LeftKey: "uid", RightKey: "uid",
		LeftRepeat: NewRat(1), RightRepeat: NewRat(2)}
	count := &BoundedCount{ID: "c", In: join, Cap: 100}

	tables := sampleTables()
	usersTab := tables["users"]
	usersTab.Rows = append(usersTab.Rows,
		Row{Values: []Cell{NewRat(1), NewRat(8)}})
	tables["users"] = usersTab

	report, err := Verify(count, tables, map[string][]Row{
		"events": {{Values: []Cell{NewRat(1), NewRat(10)}}},
	})
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if report.Violation {
		t.Fatalf("RightRepeat=2 应容纳放大 2:\n%s", report.Render())
	}
	if report.MaxScalarDelta == nil || report.MaxScalarDelta.Cmp(NewRat(2)) != 0 {
		t.Fatalf("经验最大计数差 = %v, want 2", report.MaxScalarDelta)
	}
}

func TestVerifyDetectsDeclaredBoundViolation(t *testing.T) {
	events := &Source{Rel: "events", Cols: []string{"uid", "amount"}}
	users := &Source{Rel: "users", Cols: []string{"uid", "city"}}
	join := &Join{Left: events, Right: users, LeftKey: "uid", RightKey: "uid",
		LeftRepeat: NewRat(1), RightRepeat: NewRat(1)}

	tables := sampleTables()
	usersTab := tables["users"]
	usersTab.Rows = append(usersTab.Rows,
		Row{Values: []Cell{NewRat(1), NewRat(8)}})
	tables["users"] = usersTab

	report, err := Verify(join, tables, nil)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if !report.Violation {
		t.Fatalf("实际重复 2 超过声明 1 应报告违规:\n%s", report.Render())
	}
	found := false
	for _, bc := range report.BoundChecks {
		if bc.Violation && bc.Side == "右" && bc.Observed == 2 {
			found = true
		}
	}
	if !found {
		t.Fatalf("违规报告未包含右重复上界检查: %+v", report.BoundChecks)
	}
}

func TestVerifyUnknownJoinRecordsEmpiricalImpact(t *testing.T) {
	events := &Source{Rel: "events", Cols: []string{"uid", "amount"}}
	users := &Source{Rel: "users", Cols: []string{"uid", "city"}}
	join := &Join{Left: events, Right: users, LeftKey: "uid", RightKey: "uid",
		LeftRepeat: NewRat(1)} // RightRepeat 缺失 → 未知

	report, err := Verify(join, sampleTables(), nil)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if report.Violation {
		t.Fatalf("未知节点只记录经验影响，不应判违规:\n%s", report.Render())
	}
	if report.Root.Sens.Known {
		t.Fatalf("根节点应未知")
	}
	for _, c := range report.Cases {
		for _, im := range c.Impacts {
			if !im.Sens.Known && !im.Unchecked {
				t.Fatalf("未知节点应标记 Unchecked: %+v", im)
			}
		}
	}
	if report.CheckedNeighbors != 6 {
		t.Fatalf("邻居数 = %d, want 6", report.CheckedNeighbors)
	}
}

func TestVerifyRationalClampAndDelta(t *testing.T) {
	src := &Source{Rel: "events", Cols: []string{"uid", "amount"}}
	sum := &TruncatedSum{In: src, Col: "amount",
		Lower: rat(t, "-1/2"), Upper: rat(t, "3/2")}

	tables := map[string]Table{
		"events": {
			Name:   "events",
			Schema: []string{"uid", "amount"},
			Rows: []Row{
				{Values: []Cell{NewRat(1), rat(t, "5")}},
				{Values: []Cell{NewRat(2), rat(t, "-5")}},
			},
		},
	}
	res, err := Eval(sum, tables)
	if err != nil {
		t.Fatalf("Eval: %v", err)
	}
	// clamp(5)=3/2, clamp(-5)=-1/2，合计 1。
	assertRat(t, res.Scalar, "1", "有理数截断求和")

	tr, err := Derive(sum)
	if err != nil {
		t.Fatalf("Derive: %v", err)
	}
	assertRat(t, tr.Sens.Bound, "3/2", "单行贡献上界 max(1/2,3/2)")

	report, err := Verify(sum, tables, map[string][]Row{
		"events": {{Values: []Cell{NewRat(9), rat(t, "7")}}},
	})
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if report.Violation {
		t.Fatalf("不应违规:\n%s", report.Render())
	}
	assertRat(t, report.MaxScalarDelta, "3/2", "经验最大差值受截断约束")
}

func TestVerifyTableRootDistance(t *testing.T) {
	events := &Source{Rel: "events", Cols: []string{"uid", "amount"}}
	proj := &Project{In: events, Drop: []string{"amount"}}

	report, err := Verify(proj, sampleTables(), map[string][]Row{
		"events": {{Values: []Cell{NewRat(9), NewRat(1)}}},
	})
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if report.Violation {
		t.Fatalf("投影核验不应违规:\n%s", report.Render())
	}
	if report.MaxTableDistance != 1 {
		t.Fatalf("投影根节点行距离 = %d, want 1", report.MaxTableDistance)
	}
}

func TestVerifyReportRender(t *testing.T) {
	src := &Source{ID: "e", Rel: "events", Cols: []string{"uid", "amount"}}
	count := &BoundedCount{ID: "c", In: src, Cap: 10}

	tr, err := Derive(count)
	if err != nil {
		t.Fatalf("Derive: %v", err)
	}
	if !strings.Contains(tr.Render(), "依据") {
		t.Fatalf("推导渲染应含依据说明:\n%s", tr.Render())
	}

	report, err := Verify(count, sampleTables(), nil)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	rendered := report.Render()
	for _, want := range []string{"相邻输入核验报告", "枚举邻居数", "邻居 删除 events", "结论"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("报告缺少 %q:\n%s", want, rendered)
		}
	}
}
