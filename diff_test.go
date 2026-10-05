package permdiff

import (
	"strings"
	"testing"
)

func mustParse(t *testing.T, s string) Cond {
	t.Helper()
	c, err := ParseCond([]byte(s))
	if err != nil {
		t.Fatalf("解析条件失败: %v", err)
	}
	return c
}

func TestTriLogic(t *testing.T) {
	cases := []struct {
		a, b, and, or Tri
	}{
		{True, True, True, True},
		{True, False, False, True},
		{True, Unknown, Unknown, True},
		{False, Unknown, False, Unknown},
		{Unknown, Unknown, Unknown, Unknown},
		{False, False, False, False},
	}
	for _, c := range cases {
		if got := AndTri(c.a, c.b); got != c.and {
			t.Errorf("AndTri(%v, %v) = %v, 期望 %v", c.a, c.b, got, c.and)
		}
		if got := OrTri(c.a, c.b); got != c.or {
			t.Errorf("OrTri(%v, %v) = %v, 期望 %v", c.a, c.b, got, c.or)
		}
	}
	if NotTri(Unknown) != Unknown {
		t.Errorf("NotTri(unknown) 应为 unknown")
	}
}

func TestMissingAttrIsUnknownAndDenied(t *testing.T) {
	rule := mustParse(t, `{"and": [
		{"intRange": {"attr": "level", "min": 3, "max": 7}},
		{"bool": {"attr": "mfa", "value": true}}
	]}`)
	// 缺失 mfa：整体为 unknown，最终拒绝。
	v, ex := rule.Eval(Assignment{"level": IntValue(5)})
	if v != Unknown {
		t.Fatalf("缺失属性应得 unknown，得到 %v", v)
	}
	if ok, _ := Decide(rule, Assignment{"level": IntValue(5)}); ok {
		t.Fatalf("unknown 必须被拒绝")
	}
	if !strings.Contains(ex.String(), "unknown") {
		t.Fatalf("解释树应包含 unknown 节点:\n%s", ex.String())
	}
	// 补齐属性后允许。
	if ok, _ := Decide(rule, Assignment{"level": IntValue(5), "mfa": BoolValue(true)}); !ok {
		t.Fatalf("满足全部条件应允许")
	}
}

func TestDiffIntegerRangeExpansion(t *testing.T) {
	oldC := mustParse(t, `{"intRange": {"attr": "level", "min": 5, "max": 10}}`)
	newC := mustParse(t, `{"intRange": {"attr": "level", "min": 3, "max": 10}}`)
	res := Diff(oldC, newC, Config{IntMin: 0, IntMax: 10})
	if !res.Expands {
		t.Fatalf("区间下扩应判定为扩大")
	}
	v, ok := res.Witness["level"]
	if !ok || v.Kind != KindInt || v.Int != 3 {
		t.Fatalf("最小见证应为 level=3，得到 %v", res.Witness)
	}
	if res.NewValue != True || res.OldValue != False {
		t.Fatalf("见证下新规则应 true、旧规则应 false，得到 %v/%v", res.NewValue, res.OldValue)
	}
}

func TestDiffNoExpansion(t *testing.T) {
	oldC := mustParse(t, `{"intRange": {"attr": "level", "min": 3, "max": 10}}`)
	newC := mustParse(t, `{"intRange": {"attr": "level", "min": 5, "max": 10}}`)
	res := Diff(oldC, newC, Config{IntMin: 0, IntMax: 10})
	if res.Expands {
		t.Fatalf("区间收缩不应判定为扩大，见证: %v", res.Witness)
	}
}

func TestDiffMissingAttrBecomesWitness(t *testing.T) {
	// 旧规则额外要求 mfa，新规则不要求：
	// 最小见证是只给 level，mfa 缺失使旧规则为 unknown（即拒绝）。
	oldC := mustParse(t, `{"and": [
		{"intRange": {"attr": "level", "min": 3, "max": 10}},
		{"bool": {"attr": "mfa", "value": true}}
	]}`)
	newC := mustParse(t, `{"intRange": {"attr": "level", "min": 3, "max": 10}}`)
	res := Diff(oldC, newC, Config{IntMin: 0, IntMax: 10})
	if !res.Expands {
		t.Fatalf("去掉 mfa 要求应判定为扩大")
	}
	if len(res.Witness) != 1 {
		t.Fatalf("最小见证应只含 level，得到 %v", res.Witness)
	}
	if _, has := res.Witness["mfa"]; has {
		t.Fatalf("见证不应包含 mfa: %v", res.Witness)
	}
	if res.OldValue != Unknown {
		t.Fatalf("旧规则在见证下应为 unknown，得到 %v", res.OldValue)
	}
}

func TestDiffLabelSetExpansion(t *testing.T) {
	oldC := mustParse(t, `{"containsAll": {"attr": "groups", "set": ["eng", "ops"]}}`)
	newC := mustParse(t, `{"contains": {"attr": "groups", "elem": "eng"}}`)
	res := Diff(oldC, newC, Config{})
	if !res.Expands {
		t.Fatalf("放宽标签要求应判定为扩大")
	}
	v, ok := res.Witness["groups"]
	if !ok || v.Kind != KindSet {
		t.Fatalf("见证应包含 groups 集合，得到 %v", res.Witness)
	}
	if len(v.Set) != 1 || !v.Set["eng"] {
		t.Fatalf("最小见证应为 groups={eng}，得到 %v", v.Elems())
	}
}

func TestDiffLabelSubsetExpansion(t *testing.T) {
	oldC := mustParse(t, `{"subsetOf": {"attr": "tags", "set": ["public"]}}`)
	newC := mustParse(t, `{"subsetOf": {"attr": "tags", "set": ["public", "internal"]}}`)
	res := Diff(oldC, newC, Config{})
	if !res.Expands {
		t.Fatalf("扩大允许的标签集合应判定为扩大")
	}
	v := res.Witness["tags"]
	if len(v.Set) != 1 || !v.Set["internal"] {
		t.Fatalf("最小见证应为 tags={internal}，得到 %v", v.Elems())
	}
}

func TestDiffIdenticalRules(t *testing.T) {
	r := `{"or": [
		{"intRange": {"attr": "level", "min": 5, "max": 9}},
		{"contains": {"attr": "groups", "elem": "admin"}}
	]}`
	res := Diff(mustParse(t, r), mustParse(t, r), Config{IntMin: 0, IntMax: 10})
	if res.Expands {
		t.Fatalf("相同规则不应判定为扩大，见证: %v", res.Witness)
	}
}

func TestDiffNotCondition(t *testing.T) {
	oldC := mustParse(t, `{"not": {"contains": {"attr": "groups", "elem": "banned"}}}`)
	newC := mustParse(t, `{"or": [
		{"not": {"contains": {"attr": "groups", "elem": "banned"}}},
		{"contains": {"attr": "groups", "elem": "auditor"}}
	]}`)
	res := Diff(oldC, newC, Config{})
	if !res.Expands {
		t.Fatalf("新增 auditor 分支应判定为扩大")
	}
	v := res.Witness["groups"]
	// 最小见证是 {auditor, banned}：auditor 让新规则为真，
	// banned 让旧规则的 NOT 条件为假。
	if !v.Set["auditor"] || !v.Set["banned"] || len(v.Set) != 2 {
		t.Fatalf("最小见证应为 groups={auditor, banned}，得到 %v", v.Elems())
	}
	if res.OldValue != False {
		t.Fatalf("旧规则在见证下应为 false，得到 %v", res.OldValue)
	}
}

func TestExplanationTreeShape(t *testing.T) {
	rule := mustParse(t, `{"and": [
		{"intRange": {"attr": "level", "min": 3, "max": 7}},
		{"or": [
			{"bool": {"attr": "mfa", "value": true}},
			{"contains": {"attr": "groups", "elem": "admin"}}
		]}
	]}`)
	_, ex := rule.Eval(Assignment{"level": IntValue(5), "groups": SetValue("admin")})
	s := ex.String()
	for _, want := range []string{"AND => true", "OR => true", "mfa == true => unknown", "groups ∋ \"admin\" => true"} {
		if !strings.Contains(s, want) {
			t.Errorf("解释树缺少 %q:\n%s", want, s)
		}
	}
}
