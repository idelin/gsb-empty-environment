package policydiff

import (
	"strings"
	"testing"
)

func testSchema() Schema {
	return Schema{
		"subject.level":  {Type: "int", Min: 0, Max: 10},
		"resource.label": {Type: "label", Values: []string{"public", "internal", "secret"}},
		"subject.mfa":    {Type: "bool"},
	}
}

func TestKleeneConnectives(t *testing.T) {
	cases := []struct {
		op   string
		a, b Tri
		want Tri
	}{
		{"and", True, True, True},
		{"and", True, Unknown, Unknown},
		{"and", False, Unknown, False}, // 不能按二值逻辑化简：False 吸收 Unknown
		{"and", Unknown, Unknown, Unknown},
		{"or", True, Unknown, True},
		{"or", False, Unknown, Unknown},
		{"or", False, False, False},
	}
	for _, c := range cases {
		var got Tri
		switch c.op {
		case "and":
			got = and3(c.a, c.b)
		case "or":
			got = or3(c.a, c.b)
		}
		if got != c.want {
			t.Errorf("%s(%v, %v) = %v, want %v", c.op, c.a, c.b, got, c.want)
		}
	}
	if not3(Unknown) != Unknown || not3(True) != False || not3(False) != True {
		t.Error("not3 三值语义错误")
	}
}

func TestEvalMissingAttrIsUnknown(t *testing.T) {
	e := IntRange("subject.level", 1, 5)
	if got := Eval(e, Env{}); got != Unknown {
		t.Fatalf("缺失属性应得 Unknown，实际 %v", got)
	}
	if Grants(e, Env{}) {
		t.Fatal("Unknown 必须按拒绝处理")
	}
	env := Env{"subject.level": Int(3)}
	if Eval(e, env) != True {
		t.Fatal("范围内取值应放行")
	}
	if Eval(e, Env{"subject.level": Int(9)}) != False {
		t.Fatal("范围外取值应拒绝")
	}
}

func TestEvalLabelAndBool(t *testing.T) {
	l := LabelIn("resource.label", "public", "internal")
	if Eval(l, Env{"resource.label": Label("internal")}) != True {
		t.Fatal("标签应在集合内")
	}
	if Eval(l, Env{"resource.label": Label("secret")}) != False {
		t.Fatal("标签不应在集合内")
	}
	if Eval(l, Env{}) != Unknown {
		t.Fatal("缺失标签属性应为 Unknown")
	}
	b := BoolCond("subject.mfa", true)
	if Eval(b, Env{"subject.mfa": Bool(true)}) != True {
		t.Fatal("布尔条件应成立")
	}
	if Eval(b, Env{"subject.mfa": Bool(false)}) != False {
		t.Fatal("布尔条件应不成立")
	}
}

func TestNoExpansionWhenSubset(t *testing.T) {
	old := IntRange("subject.level", 0, 10)
	newRule := IntRange("subject.level", 3, 5)
	res, err := CheckExpansion(testSchema(), old, newRule)
	if err != nil {
		t.Fatal(err)
	}
	if res.Expands {
		t.Fatalf("新规则是旧规则的子集，不应扩大；反例=%v", res.Counterexample)
	}
}

func TestExpansionFindsMinimalCounterexample(t *testing.T) {
	old := IntRange("subject.level", 8, 10)
	newRule := IntRange("subject.level", 5, 10)
	res, err := CheckExpansion(testSchema(), old, newRule)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Expands {
		t.Fatal("应判定为扩大授权")
	}
	if len(res.Counterexample) != 1 {
		t.Fatalf("最小反例应只含 1 个属性，实际 %v", res.Counterexample)
	}
	got := res.Counterexample["subject.level"]
	if got.Kind != IntKind || got.Int != 5 {
		t.Fatalf("最小反例应为 subject.level = 5，实际 %v", got)
	}
	if res.NewTree.Value != True {
		t.Fatalf("新规则解释树根应为 True，实际 %v", res.NewTree.Value)
	}
	if res.OldTree.Value == True {
		t.Fatal("旧规则解释树根不应为 True")
	}
}

func TestExpansionViaMissingAttribute(t *testing.T) {
	// 旧规则引用 subject.mfa，新规则不引用：
	// 请求缺少 mfa 时旧规则为 Unknown → 拒绝，新规则放行。
	old := And(IntRange("subject.level", 0, 10), BoolCond("subject.mfa", true))
	newRule := IntRange("subject.level", 0, 10)
	res, err := CheckExpansion(testSchema(), old, newRule)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Expands {
		t.Fatal("缺失属性导致旧规则 Unknown，应判定为扩大")
	}
	if _, ok := res.Counterexample["subject.mfa"]; ok {
		t.Fatalf("最小反例不应包含 subject.mfa：%v", res.Counterexample)
	}
	if res.OldTree.Value != Unknown {
		t.Fatalf("旧规则应为 Unknown，实际 %v", res.OldTree.Value)
	}
	if !strings.Contains(res.OldTree.String(), "缺失") {
		t.Fatalf("解释树应说明属性缺失：\n%s", res.OldTree.String())
	}
}

func TestMinimalityPrefersFewerAttributes(t *testing.T) {
	// 新规则两条路径：一条只需 label，一条需要 level+mfa。
	old := LabelIn("resource.label", "public")
	newRule := Or(
		LabelIn("resource.label", "internal"),
		And(IntRange("subject.level", 9, 10), BoolCond("subject.mfa", true)),
	)
	res, err := CheckExpansion(testSchema(), old, newRule)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Expands {
		t.Fatal("应判定为扩大授权")
	}
	if len(res.Counterexample) != 1 {
		t.Fatalf("最小反例应只含 1 个属性，实际 %v", res.Counterexample)
	}
	if got := res.Counterexample["resource.label"]; got.Label != "internal" {
		t.Fatalf("最小反例应为 resource.label = internal，实际 %v", got)
	}
}

func TestNoExpansionWhenNewUnsatisfiable(t *testing.T) {
	old := IntRange("subject.level", 0, 2)
	newRule := And(IntRange("subject.level", 0, 2), IntRange("subject.level", 5, 7))
	res, err := CheckExpansion(testSchema(), old, newRule)
	if err != nil {
		t.Fatal(err)
	}
	if res.Expands {
		t.Fatal("新规则不可满足，不应扩大")
	}
}

func TestMissingDomainIsError(t *testing.T) {
	_, err := CheckExpansion(Schema{}, IntRange("ghost", 0, 1), IntRange("ghost", 0, 2))
	if err == nil || !strings.Contains(err.Error(), "ghost") {
		t.Fatalf("缺少域定义应报错，实际 %v", err)
	}
}

func TestJSONRoundTrip(t *testing.T) {
	rule, err := ParseRule([]byte(`{
	  "and": [
	    {"intRange": {"attr": "subject.level", "lo": 5, "hi": 10}},
	    {"or": [
	      {"labelIn": {"attr": "resource.label", "set": ["public", "internal"]}},
	      {"bool": {"attr": "subject.mfa", "value": true}}
	    ]},
	    {"not": {"labelIn": {"attr": "resource.label", "set": ["secret"]}}}
	  ]
	}`))
	if err != nil {
		t.Fatal(err)
	}
	env := Env{"subject.level": Int(7), "resource.label": Label("public")}
	if Eval(rule, env) != True {
		t.Fatal("JSON 规则求值应为 True")
	}
	schema, err := ParseSchema([]byte(`{"attributes": {
	  "subject.level": {"type": "int", "min": 0, "max": 10},
	  "resource.label": {"type": "label", "values": ["public", "internal", "secret"]},
	  "subject.mfa": {"type": "bool"}
	}}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(schema) != 3 {
		t.Fatalf("模式应含 3 个属性，实际 %d", len(schema))
	}
	if _, err := ParseRule([]byte(`{"intRange": {"attr": "x", "lo": 9, "hi": 1}}`)); err == nil {
		t.Fatal("下界大于上界应报错")
	}
}

func TestExplainTreeStructure(t *testing.T) {
	rule := And(IntRange("subject.level", 5, 10), LabelIn("resource.label", "public"))
	env := Env{"subject.level": Int(7)}
	root := Explain(rule, env)
	if root.Value != Unknown {
		t.Fatalf("根应为 Unknown（label 缺失），实际 %v", root.Value)
	}
	if len(root.Children) != 2 {
		t.Fatalf("AND 应有 2 个子节点，实际 %d", len(root.Children))
	}
	if root.Children[0].Value != True || root.Children[1].Value != Unknown {
		t.Fatalf("子节点真值错误：%v, %v", root.Children[0].Value, root.Children[1].Value)
	}
	out := root.String()
	if !strings.Contains(out, "AND") || !strings.Contains(out, "缺失") {
		t.Fatalf("解释树渲染缺少关键信息：\n%s", out)
	}
}
