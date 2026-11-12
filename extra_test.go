package netsec

import (
	"fmt"
	"strings"
	"testing"
)

func TestStarOnlyConsumesInt(t *testing.T) {
	r := AnalyzeFormat("%*s")
	want := []Consumption{
		{Index: 0, Role: RoleWidth, Verb: 's', Constraint: KindInt},
		{Index: 1, Role: RoleValue, Verb: 's', Constraint: KindString},
	}
	assertConsumptions(t, r, want)
}

func TestExplicitWidthKeepsVerbIndex(t *testing.T) {
	// %[2]*[1]d：参数 2 提供宽度，参数 1 是整数值。
	r := AnalyzeFormat("%[2]*[1]d")
	if r.MixedModes {
		t.Fatalf("不应判定混用：%+v", r.Diagnostics)
	}
	want := []Consumption{
		{Index: 1, Explicit: true, Role: RoleWidth, Verb: 'd', Constraint: KindInt},
		{Index: 0, Explicit: true, Role: RoleValue, Verb: 'd', Constraint: KindInt},
	}
	assertConsumptions(t, r, want)
}

func TestExplicitPrecisionStar(t *testing.T) {
	r := AnalyzeFormat("%.[2]*[1]s")
	want := []Consumption{
		{Index: 1, Explicit: true, Role: RolePrecision, Verb: 's', Constraint: KindInt},
		{Index: 0, Explicit: true, Role: RoleValue, Verb: 's', Constraint: KindString, Truncating: true},
	}
	assertConsumptions(t, r, want)
}

func TestRuntimeCrossCheck(t *testing.T) {
	type tc struct {
		format string
		args   []any
		kinds  []Kind
	}
	cases := []tc{
		{"%*s", []any{4, "ab"}, []Kind{KindInt, KindString}},
		{"%[2]*[1]d", []any{42, 6}, []Kind{KindInt, KindInt}},
		{"%.[2]*[1]s", []any{"abcdef", 3}, []Kind{KindString, KindInt}},
		{"%[3]*.[2]*[1]s", []any{"abcdef", 2, 5}, []Kind{KindString, KindInt, KindInt}},
		{"%d-%[1]d", []any{9}, []Kind{KindInt}},
	}
	for _, c := range cases {
		out := fmt.Sprintf(c.format, c.args...)
		if strings.Contains(out, "%!") || strings.Contains(out, "(MISSING)") || strings.Contains(out, "EXTRA") {
			t.Fatalf("%s 运行时格式错误：%s", c.format, out)
		}
		r := AnalyzeFormat(c.format)
		if idx := r.TypeMismatch(c.kinds); idx != -1 {
			t.Fatalf("%s 类型误报于 %d", c.format, idx)
		}
		it, err := Interpret(c.format)
		if err != nil {
			t.Fatalf("%s 解释器失败：%v", c.format, err)
		}
		if it.ArgCount != r.ArgCount() {
			t.Fatalf("%s 参数数不一致", c.format)
		}
	}
}

func TestRepeatedExplicitRuntime(t *testing.T) {
	r := AnalyzeFormat("%[1]d-%[1]d")
	if got := r.RepeatedPositions(); len(got) != 1 || got[0] != 0 {
		t.Fatalf("重复位置 = %v", got)
	}
	if fmt.Sprintf("%[1]d-%[1]d", 7) != "7-7" {
		t.Fatal("运行时重复位置行为异常")
	}
}

func TestExplicitArgCountCoversGaps(t *testing.T) {
	// 只引用第 3 个参数时，ArgCount 仍应覆盖到位置 2（共 3 个槽位）。
	r := AnalyzeFormat("%[3]d")
	if r.ArgCount() != 3 {
		t.Fatalf("ArgCount = %d，期望 3", r.ArgCount())
	}
	if r.Constraint(2) != KindInt {
		t.Fatalf("位置 2 约束 = %v，期望 KindInt", r.Constraint(2))
	}
	if r.Constraint(0) != KindUnknown {
		t.Fatalf("未消费位置应为 KindUnknown")
	}
}

func TestQuotedStringTruncation(t *testing.T) {
	r := AnalyzeFormat("%.2q")
	if got := r.TruncatingPositions(); len(got) != 1 || got[0] != 0 {
		t.Fatalf("截断位置 = %v，期望 [0]", got)
	}
}
