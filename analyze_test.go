package netsec

import (
	"fmt"
	"strings"
	"testing"
)

func TestSequentialConsumption(t *testing.T) {
	r := AnalyzeFormat("%d %s %*.*f")
	if !r.Guaranteed || len(r.Diagnostics) != 0 {
		t.Fatalf("期望无诊断且可保证，得到 %+v", r.Diagnostics)
	}
	want := []Consumption{
		{Index: 0, Role: RoleValue, Verb: 'd', Constraint: KindInt},
		{Index: 1, Role: RoleValue, Verb: 's', Constraint: KindString},
		{Index: 2, Role: RoleWidth, Verb: 'f', Constraint: KindInt},
		{Index: 3, Role: RolePrecision, Verb: 'f', Constraint: KindInt},
		{Index: 4, Role: RoleValue, Verb: 'f', Constraint: KindFloat},
	}
	assertConsumptions(t, r, want)
	if r.ArgCount() != 5 {
		t.Fatalf("ArgCount = %d，期望 5", r.ArgCount())
	}
}

func TestExplicitConsumption(t *testing.T) {
	r := AnalyzeFormat("%[2]d %[1]s")
	if !r.ExplicitMode || r.MixedModes {
		t.Fatalf("模式标记错误：%+v", r)
	}
	want := []Consumption{
		{Index: 1, Explicit: true, Role: RoleValue, Verb: 'd', Constraint: KindInt},
		{Index: 0, Explicit: true, Role: RoleValue, Verb: 's', Constraint: KindString},
	}
	assertConsumptions(t, r, want)
	if r.ArgCount() != 2 {
		t.Fatalf("ArgCount = %d，期望 2", r.ArgCount())
	}
}

func TestExplicitDynamicWidthAndPrecision(t *testing.T) {
	r := AnalyzeFormat("%[3]*.[2]*[1]s")
	if r.MixedModes {
		t.Fatalf("纯显式模式不应判定混用：%+v", r.Diagnostics)
	}
	want := []Consumption{
		{Index: 2, Explicit: true, Role: RoleWidth, Verb: 's', Constraint: KindInt},
		{Index: 1, Explicit: true, Role: RolePrecision, Verb: 's', Constraint: KindInt},
		{Index: 0, Explicit: true, Role: RoleValue, Verb: 's', Constraint: KindString, Truncating: true},
	}
	assertConsumptions(t, r, want)
	if got := r.TruncatingPositions(); len(got) != 1 || got[0] != 0 {
		t.Fatalf("截断位置 = %v，期望 [0]", got)
	}
}

func TestMixedModesRejected(t *testing.T) {
	cases := []string{
		"%d %[2]s",
		"%[1]d %s",
		"%*d %[2]s",
		"%[2]*d %s",
	}
	for _, format := range cases {
		r := AnalyzeFormat(format)
		if !r.MixedModes || !hasCode(r, DiagMixedModes) {
			t.Fatalf("%s 未报告混用：%+v", format, r.Diagnostics)
		}
	}
}

func TestRepeatedPositions(t *testing.T) {
	r := AnalyzeFormat("%[1]d %[1]d %[2]s")
	got := r.RepeatedPositions()
	if len(got) != 1 || got[0] != 0 {
		t.Fatalf("重复位置 = %v，期望 [0]", got)
	}
	var repeated int
	for _, c := range r.Consumptions {
		if c.Repeated {
			repeated++
		}
	}
	if repeated != 1 {
		t.Fatalf("Repeated 标记数 = %d，期望 1", repeated)
	}
}

func TestTruncatingPositions(t *testing.T) {
	r := AnalyzeFormat("%.3s %.*s %s")
	got := r.TruncatingPositions()
	if len(got) != 2 || got[0] != 0 || got[1] != 2 {
		t.Fatalf("截断位置 = %v，期望 [0 2]", got)
	}
}

func TestTypeConflictAndMismatch(t *testing.T) {
	r := AnalyzeFormat("%[1]d %[1]s")
	if !hasCode(r, DiagTypeConflict) {
		t.Fatalf("期望类型冲突诊断：%+v", r.Diagnostics)
	}
	ok := AnalyzeFormat("%d %s")
	if idx := ok.TypeMismatch([]Kind{KindInt, KindString}); idx != -1 {
		t.Fatalf("正确类型被误报错位，位置 %d", idx)
	}
	if idx := ok.TypeMismatch([]Kind{KindString, KindString}); idx != 0 {
		t.Fatalf("错位位置 = %d，期望 0", idx)
	}
}

func TestTaintedNotGuaranteed(t *testing.T) {
	r := Analyze("%d", true)
	if r.Guaranteed || !r.Tainted || !hasCode(r, DiagTainted) {
		t.Fatalf("受污格式应无法静态保证：%+v", r)
	}
	if len(r.Consumptions) != 1 {
		t.Fatalf("受污格式仍应尽力给出消费记录")
	}
}

func TestMalformed(t *testing.T) {
	cases := []string{
		"%",
		"%[3",
		"%[0]d",
		"%[3]2d",
		"%[3].2d",
		"%[1].*s",
		"%[1].*",
	}
	for _, format := range cases {
		r := AnalyzeFormat(format)
		if r.Guaranteed {
			t.Fatalf("%s 应当不可静态保证", format)
		}
	}
}

func TestLiteralPercent(t *testing.T) {
	r := AnalyzeFormat("100%% done %d")
	if r.ArgCount() != 1 || r.Constraint(0) != KindInt {
		t.Fatalf("%% 不应消费参数：%+v", r.Consumptions)
	}
}

func assertConsumptions(t *testing.T, r *Result, want []Consumption) {
	t.Helper()
	if len(r.Consumptions) != len(want) {
		t.Fatalf("消费数 = %d，期望 %d：%+v", len(r.Consumptions), len(want), r.Consumptions)
	}
	for i, w := range want {
		got := r.Consumptions[i]
		if got.Index != w.Index || got.Explicit != w.Explicit ||
			got.Role != w.Role || got.Verb != w.Verb ||
			got.Constraint != w.Constraint || got.Truncating != w.Truncating {
			t.Fatalf("消费[%d] = %+v，期望 %+v", i, got, w)
		}
	}
}

func hasCode(r *Result, code string) bool {
	for _, d := range r.Diagnostics {
		if d.Code == code {
			return true
		}
	}
	return false
}

func TestInterpreterAgrees(t *testing.T) {
	formats := []string{
		"%d %s",
		"%*.*f",
		"%[2]d %[1]s",
		"%[3]*.[2]*[1]s",
		"%[1]d %[1]d",
		"%.3s %.*q",
		"prefix %[1]v %% literal",
		"%08.3x %-*s",
	}
	for _, format := range formats {
		r := AnalyzeFormat(format)
		it, err := Interpret(format)
		if err != nil {
			t.Fatalf("%s 解释器报错：%v", format, err)
		}
		if len(it.Events) != len(r.Consumptions) {
			t.Fatalf("%s 事件数不一致：%d vs %d", format, len(it.Events), len(r.Consumptions))
		}
		for i, e := range it.Events {
			c := r.Consumptions[i]
			if e.Index != c.Index || e.Explicit != c.Explicit ||
				e.Role != c.Role || e.Verb != c.Verb ||
				e.Constraint != c.Constraint || e.Truncating != c.Truncating {
				t.Fatalf("%s 事件[%d] 不一致：%+v vs %+v", format, i, e, c)
			}
		}
		assertSameSet(t, format, it.Repeated, r.RepeatedPositions())
		assertSameSet(t, format, it.Truncating, r.TruncatingPositions())
		if it.ArgCount != r.ArgCount() {
			t.Fatalf("%s ArgCount 不一致：%d vs %d", format, it.ArgCount, r.ArgCount())
		}
	}
}

func TestInterpreterRejectsMalformed(t *testing.T) {
	for _, format := range []string{"%", "%[3", "%[0]d", "%[3]2d", "%[1].*s"} {
		if _, err := Interpret(format); err == nil {
			t.Fatalf("%s 应被解释器拒绝", format)
		}
	}
}

func assertSameSet(t *testing.T, format string, a, b []int) {
	t.Helper()
	if len(a) != len(b) {
		t.Fatalf("%s 位置集合长度不一致：%v vs %v", format, a, b)
	}
	for i := range a {
		if a[i] != b[i] {
			t.Fatalf("%s 位置集合不一致：%v vs %v", format, a, b)
		}
	}
}

// TestRuntimeSemantics 用标准库 fmt.Sprintf 交叉验证顺序模式的消费次序。
func TestRuntimeSemantics(t *testing.T) {
	type tc struct {
		format string
		args   []any
	}
	cases := []tc{
		{"%d %s", []any{7, "ab"}},
		{"%*.*f", []any{8, 2, 1.5}},
		{"%[2]d %[1]s", []any{"ab", 7}},
		{"%[3]*.[2]*[1]s", []any{"abcdef", 2, 4}},
		{"%08x %-*s", []any{255, 6, "ab"}},
		{"%.3s", []any{"abcdef"}},
	}
	for _, c := range cases {
		out := fmt.Sprintf(c.format, c.args...)
		if strings.Contains(out, "%!") || strings.Contains(out, "(MISSING)") || strings.Contains(out, "EXTRA") {
			t.Fatalf("%s 运行时出现格式错误：%s", c.format, out)
		}
	}
}
