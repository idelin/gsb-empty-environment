package sensitivity

import (
	"strings"
	"testing"
)

func rat(t *testing.T, s string) Cell {
	t.Helper()
	r, err := ParseRat(s)
	if err != nil {
		t.Fatalf("ParseRat(%q): %v", s, err)
	}
	return r
}

func assertRat(t *testing.T, got Cell, want string, msg string) {
	t.Helper()
	w := rat(t, want)
	if got == nil || got.Cmp(w) != 0 {
		t.Fatalf("%s: got %v want %s", msg, got, want)
	}
}

func sampleTables() map[string]Table {
	return map[string]Table{
		"events": {
			Name:   "events",
			Schema: []string{"uid", "amount"},
			Rows: []Row{
				{Values: []Cell{NewRat(1), NewRat(10)}},
				{Values: []Cell{NewRat(2), NewRat(-50)}},
				{Values: []Cell{NewRat(3), NewRat(5)}},
			},
		},
		"users": {
			Name:   "users",
			Schema: []string{"uid", "city"},
			Rows: []Row{
				{Values: []Cell{NewRat(1), NewRat(7)}},
				{Values: []Cell{NewRat(2), NewRat(7)}},
				{Values: []Cell{NewRat(3), NewRat(9)}},
			},
		},
	}
}

func TestDeriveLeafAndProject(t *testing.T) {
	src := &Source{ID: "s1", Rel: "events", Cols: []string{"uid", "amount"}}
	proj := &Project{ID: "p1", In: src, Drop: []string{"amount"}}

	tr, err := Derive(proj)
	if err != nil {
		t.Fatalf("Derive: %v", err)
	}
	if got := tr.Schema; len(got) != 1 || got[0] != "uid" {
		t.Fatalf("投影输出列 = %v, want [uid]", got)
	}
	if !tr.Sens.Known {
		t.Fatalf("投影敏感度应已知: %s", tr.Sens)
	}
	assertRat(t, tr.Sens.Rows, "1", "投影行级敏感度")
	if !strings.Contains(tr.Render(), "Project[p1]") {
		t.Fatalf("渲染缺少节点名:\n%s", tr.Render())
	}
}

func TestDeriveBoundedCount(t *testing.T) {
	src := &Source{Rel: "events", Cols: []string{"uid", "amount"}}
	count := &BoundedCount{ID: "c1", In: src, Cap: 2}

	tr, err := Derive(count)
	if err != nil {
		t.Fatalf("Derive: %v", err)
	}
	assertRat(t, tr.Sens.Bound, "1", "有界计数敏感度")

	res, err := Eval(count, sampleTables())
	if err != nil {
		t.Fatalf("Eval: %v", err)
	}
	assertRat(t, res.Scalar, "2", "3 行数据在 Cap=2 下截断为 2")

	tables := sampleTables()
	eventsTab := tables["events"]
	eventsTab.Rows = eventsTab.Rows[:1]
	tables["events"] = eventsTab
	res, err = Eval(count, tables)
	if err != nil {
		t.Fatalf("Eval: %v", err)
	}
	assertRat(t, res.Scalar, "1", "1 行数据不触发截断")
}

func TestDeriveTruncatedSum(t *testing.T) {
	src := &Source{Rel: "events", Cols: []string{"uid", "amount"}}
	sum := &TruncatedSum{ID: "sum1", In: src, Col: "amount",
		Lower: NewRat(0), Upper: NewRat(20)}

	tr, err := Derive(sum)
	if err != nil {
		t.Fatalf("Derive: %v", err)
	}
	assertRat(t, tr.Sens.Bound, "20", "截断求和敏感度")

	res, err := Eval(sum, sampleTables())
	if err != nil {
		t.Fatalf("Eval: %v", err)
	}
	assertRat(t, res.Scalar, "15", "截断求和结果 10+0+5")

	sum.Lower = NewRat(-10)
	sum.Upper = NewRat(10)
	tr, err = Derive(sum)
	if err != nil {
		t.Fatalf("Derive: %v", err)
	}
	assertRat(t, tr.Sens.Bound, "10", "对称截断敏感度")
	res, err = Eval(sum, sampleTables())
	if err != nil {
		t.Fatalf("Eval: %v", err)
	}
	assertRat(t, res.Scalar, "5", "对称截断求和结果 10-10+5")
}

func TestTruncatedSumMissingBoundUnknown(t *testing.T) {
	src := &Source{Rel: "events", Cols: []string{"uid", "amount"}}
	sum := &TruncatedSum{ID: "sum1", In: src, Col: "amount",
		Lower: nil, Upper: NewRat(20)}

	tr, err := Derive(sum)
	if err != nil {
		t.Fatalf("Derive: %v", err)
	}
	if tr.Sens.Known {
		t.Fatalf("缺少截断界时应未知，得到 %s", tr.Sens)
	}
	if !strings.Contains(tr.Sens.Reason, "Lower") {
		t.Fatalf("未知原因应指出缺失的界: %s", tr.Sens.Reason)
	}
}

func TestDeriveUnion(t *testing.T) {
	a := &Source{ID: "a", Rel: "events", Cols: []string{"uid", "amount"}}
	b := &Source{ID: "b", Rel: "events", Cols: []string{"uid", "amount"}}
	union := &Union{ID: "u1", L: a, R: b}

	tr, err := Derive(union)
	if err != nil {
		t.Fatalf("Derive: %v", err)
	}
	assertRat(t, tr.Sens.Rows, "1", "并集行级敏感度取 max(1,1)")

	bad := &Union{L: a, R: &Source{Rel: "users", Cols: []string{"uid", "city"}}}
	if _, err := Derive(bad); err == nil {
		t.Fatal("并集两侧列名不一致应报错")
	}
}

func TestDeriveJoinKnownAndUnknown(t *testing.T) {
	events := &Source{ID: "e", Rel: "events", Cols: []string{"uid", "amount"}}
	users := &Source{ID: "u", Rel: "users", Cols: []string{"uid", "city"}}

	join := &Join{ID: "j1", Left: events, Right: users,
		LeftKey: "uid", RightKey: "uid",
		LeftRepeat: NewRat(1), RightRepeat: NewRat(2)}
	tr, err := Derive(join)
	if err != nil {
		t.Fatalf("Derive: %v", err)
	}
	assertRat(t, tr.Sens.Rows, "2", "连接行级敏感度 max(1x2,1x1)")
	if len(tr.Schema) != 4 {
		t.Fatalf("连接输出列 = %v", tr.Schema)
	}

	join.RightRepeat = nil
	tr, err = Derive(join)
	if err != nil {
		t.Fatalf("Derive: %v", err)
	}
	if tr.Sens.Known || !strings.Contains(tr.Sens.Reason, "RightRepeat") {
		t.Fatalf("缺少 RightRepeat 应未知并指明原因，得到 %s", tr.Sens)
	}

	join.RightRepeat = NewRat(2)
	join.LeftRepeat = nil
	tr, _ = Derive(join)
	if tr.Sens.Known || !strings.Contains(tr.Sens.Reason, "LeftRepeat") {
		t.Fatalf("缺少 LeftRepeat 应未知并指明原因，得到 %s", tr.Sens)
	}
}

func TestUnknownPropagatesThroughSum(t *testing.T) {
	events := &Source{Rel: "events", Cols: []string{"uid", "amount"}}
	users := &Source{Rel: "users", Cols: []string{"uid", "city"}}
	join := &Join{Left: events, Right: users, LeftKey: "uid", RightKey: "uid",
		LeftRepeat: NewRat(1)}
	proj := &Project{In: join, Drop: []string{"city"}}
	sum := &TruncatedSum{In: proj, Col: "amount", Lower: NewRat(0), Upper: NewRat(20)}

	tr, err := Derive(sum)
	if err != nil {
		t.Fatalf("Derive: %v", err)
	}
	if tr.Sens.Known {
		t.Fatalf("未知连接应沿投影、求和传播为未知，得到 %s", tr.Sens)
	}
}

func TestEvalJoinSemantics(t *testing.T) {
	events := &Source{Rel: "events", Cols: []string{"uid", "amount"}}
	users := &Source{Rel: "users", Cols: []string{"uid", "city"}}
	join := &Join{Left: events, Right: users, LeftKey: "uid", RightKey: "uid",
		LeftRepeat: NewRat(2), RightRepeat: NewRat(2)}

	tables := sampleTables()
	usersTab := tables["users"]
	usersTab.Rows = append(usersTab.Rows,
		Row{Values: []Cell{NewRat(1), NewRat(8)}})
	tables["users"] = usersTab

	res, err := Eval(join, tables)
	if err != nil {
		t.Fatalf("Eval: %v", err)
	}
	if len(res.Table.Rows) != 4 {
		t.Fatalf("连接结果行数 = %d, want 4", len(res.Table.Rows))
	}

	obs, err := MaxMultiplicity(*res.Table, "uid")
	if err != nil {
		t.Fatalf("MaxMultiplicity: %v", err)
	}
	if obs != 2 {
		t.Fatalf("uid 最大重复 = %d, want 2", obs)
	}
}
