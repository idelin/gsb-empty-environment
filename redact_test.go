package netsec

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func mustSanitize(t *testing.T, roots map[string]*Node, opts Options) string {
	t.Helper()
	out, err := Sanitize(roots, opts)
	if err != nil {
		t.Fatalf("Sanitize returned error: %v", err)
	}
	// 输出必须始终是合法 JSON。
	var v any
	if err := json.Unmarshal(out, &v); err != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", err, out)
	}
	return string(out)
}

// 改变秘密子图的大小或取值，公开输出字节必须完全一致，且不泄露机密节点数量。
func TestSecretSubgraphMutationLeavesPublicOutputIdentical(t *testing.T) {
	build := func(secretPayload *Node) map[string]*Node {
		shared := String("shared-public")
		root := Dict(
			Field{Key: "pub", Value: String("hello")},
			Field{Key: "sh", Value: shared},
			Field{Key: "secret", Value: MarkSecret(Dict(
				Field{Key: "hidden-key", Value: secretPayload},
			))},
			Field{Key: "again", Value: shared},
		)
		return map[string]*Node{"event": root}
	}

	small := build(String("x"))
	large := build(Array(
		String("a"), Number(1), Number(2), Number(3),
		Dict(Field{Key: "deep", Value: MarkSecret(String("nested-secret"))}),
	))

	a := mustSanitize(t, small, Options{})
	b := mustSanitize(t, large, Options{})
	if a != b {
		t.Fatalf("public output changed with secret subgraph:\n%s\nvs\n%s", a, b)
	}
	if strings.Contains(a, "hidden-key") {
		t.Fatalf("secret dict key leaked: %s", a)
	}
	if strings.Contains(a, "nested-secret") || strings.Contains(a, `"x"`) {
		t.Fatalf("secret value leaked: %s", a)
	}
	// 占位符数量只取决于公开图中的机密入边数，两个变体应相同且为 1。
	if got := strings.Count(a, redactedPlaceholder); got != 1 {
		t.Fatalf("expected exactly one placeholder, got %d in %s", got, a)
	}
}

// 反向别名：secret 子图反向引用一个公开可达节点，该节点必须被传播为机密，
// 即使另一条完全公开的路径也引用它。
func TestReverseAliasPullsPublicNodeIntoConfidential(t *testing.T) {
	victim := String("victim-value")
	secretBox := MarkSecret(Dict(Field{Key: "back", Value: Ref(victim)}))

	root := Dict(
		Field{Key: "open", Value: victim},
		Field{Key: "box", Value: secretBox},
	)
	out := mustSanitize(t, map[string]*Node{"event": root}, Options{})

	if strings.Contains(out, "victim-value") {
		t.Fatalf("reverse-alias node value leaked: %s", out)
	}
	// "open" 键仍在（键是公开的），但其值必须被脱敏。
	if !strings.Contains(out, `"open":`+redactedPlaceholder) {
		t.Fatalf("reverse-alias reference not redacted: %s", out)
	}
}

// 公开环必须用稳定编号回引，编号只来自公开遍历顺序。
func TestPublicCycleUsesStableRefs(t *testing.T) {
	nodeA := Dict(Field{Key: "name", Value: String("A")})
	nodeB := Dict(Field{Key: "name", Value: String("B")})
	nodeA.Fields = append(nodeA.Fields, Field{Key: "next", Value: nodeB})
	nodeB.Fields = append(nodeB.Fields, Field{Key: "prev", Value: nodeA})

	out := mustSanitize(t, map[string]*Node{"event": nodeA}, Options{})
	if !strings.Contains(out, `{"$ref":0}`) {
		t.Fatalf("cycle back-reference to id 0 missing: %s", out)
	}
	// 多次序列化必须字节稳定。
	out2 := mustSanitize(t, map[string]*Node{"event": nodeA}, Options{})
	if out != out2 {
		t.Fatalf("non-deterministic output:\n%s\nvs\n%s", out, out2)
	}
}

// 机密子图内部的环绝不输出编号，只能看到固定占位符。
func TestSecretCycleDoesNotEmitIDs(t *testing.T) {
	a := MarkSecret(Dict())
	b := Dict(Field{Key: "loop", Value: a})
	a.Fields = append(a.Fields, Field{Key: "to", Value: b})

	root := Dict(Field{Key: "safe", Value: String("ok")}, Field{Key: "s", Value: a})
	out := mustSanitize(t, map[string]*Node{"event": root}, Options{})
	if strings.Contains(out, "loop") {
		t.Fatalf("secret dict key leaked: %s", out)
	}
	// 公开节点只有根 dict 与 safe 字符串（编号 0、1）；机密环不应取得任何编号。
	if strings.Count(out, `"$id"`) != 2 {
		t.Fatalf("expected exactly two public ids, got: %s", out)
	}
	if strings.Contains(out, `{"$ref":`) {
		t.Fatalf("secret cycle produced a ref: %s", out)
	}
}

// 正常共享：同一公开节点被多条路径引用时，第二次起输出 $ref，关系被保留。
func TestNormalSharingPreserved(t *testing.T) {
	shared := Array(String("leaf"))
	root := Dict(
		Field{Key: "x", Value: shared},
		Field{Key: "y", Value: shared},
	)
	out := mustSanitize(t, map[string]*Node{"event": root}, Options{})
	if !strings.Contains(out, `"y":{"$ref":1}`) {
		t.Fatalf("shared node not referenced by id: %s", out)
	}
}

// 显式 Ref 是透明别名：引用节点与直接引用共享同一编号。
func TestRefIsTransparentAlias(t *testing.T) {
	target := String("target")
	root := Dict(
		Field{Key: "direct", Value: target},
		Field{Key: "alias", Value: Ref(target)},
	)
	out := mustSanitize(t, map[string]*Node{"event": root}, Options{})
	// 键排序后 alias 在前，目标在此首次取得编号 1；direct 随后以 $ref 复用。
	if !strings.Contains(out, `"alias":{"$id":1,"$type":"string","v":"target"}`) ||
		!strings.Contains(out, `"direct":{"$ref":1}`) {
		t.Fatalf("ref did not transparently share target id: %s", out)
	}
}

// 字典按键字节排序（区分大小写，按 UTF-8 字节）。
func TestDictKeysByteSorted(t *testing.T) {
	root := Dict(
		Field{Key: "b", Value: Number(2)},
		Field{Key: "A", Value: Number(1)},
		Field{Key: "a", Value: Number(0)},
	)
	out := mustSanitize(t, map[string]*Node{"event": root}, Options{})
	iA := strings.Index(out, `"A"`)
	ia := strings.Index(out, `"a"`)
	ib := strings.Index(out, `"b"`)
	if !(iA < ia && ia < ib) {
		t.Fatalf("keys not byte-sorted: %s", out)
	}
}

// 节点预算超限：完全拒绝，不返回任何输出。
func TestNodeBudgetExceededRejectsOutput(t *testing.T) {
	root := Array(String("a"), String("b"), String("c"))
	out, err := Sanitize(map[string]*Node{"event": root}, Options{MaxNodes: 2})
	if !errors.Is(err, ErrBudgetExceeded) {
		t.Fatalf("expected ErrBudgetExceeded, got %v", err)
	}
	if out != nil {
		t.Fatalf("expected nil output on budget breach, got %s", out)
	}
}

// 字节预算超限：完全拒绝。
func TestByteBudgetExceededRejectsOutput(t *testing.T) {
	root := String(strings.Repeat("z", 500))
	out, err := Sanitize(map[string]*Node{"event": root}, Options{MaxBytes: 32})
	if !errors.Is(err, ErrBudgetExceeded) {
		t.Fatalf("expected ErrBudgetExceeded, got %v", err)
	}
	if out != nil {
		t.Fatalf("expected nil output on byte-budget breach, got %s", out)
	}
}

// NaN/Inf 无法用 JSON 表示，应判定为非法图。
func TestNonFiniteNumberRejected(t *testing.T) {
	if _, err := Sanitize(map[string]*Node{"event": Number(inf())}, Options{}); !errors.Is(err, ErrInvalidGraph) {
		t.Fatalf("expected ErrInvalidGraph for Inf, got %v", err)
	}
}

// 多个入口统一标记，机密闭包跨入口生效。
func TestConfidentialityAcrossRoots(t *testing.T) {
	leak := String("cross-root")
	roots := map[string]*Node{
		"trusted": MarkSecret(Dict(Field{Key: "x", Value: leak})),
		"public":  leak,
	}
	out := mustSanitize(t, roots, Options{})
	if strings.Contains(out, "cross-root") {
		t.Fatalf("cross-root confidential value leaked: %s", out)
	}
}

// 机密节点数量不可从编号推断：增加机密节点不会改变公开节点的编号集合。
func TestSecretCountNotInferableFromIDs(t *testing.T) {
	build := func(extraSecrets int) map[string]*Node {
		items := []*Node{String("visible")}
		for i := 0; i < extraSecrets; i++ {
			items = append(items, MarkSecret(String("hidden")))
		}
		return map[string]*Node{"event": Array(items...)}
	}
	one := mustSanitize(t, build(1), Options{})
	many := mustSanitize(t, build(50), Options{})
	// 公开部分编号保持为 0、1，仅占位符数量随机密入边变化；$id 最大值不变。
	if strings.Count(one, `"$id"`) != strings.Count(many, `"$id"`) {
		t.Fatalf("public id count leaked secret size:\n%s\nvs\n%s", one, many)
	}
	if strings.Contains(many, "hidden") {
		t.Fatalf("secret values leaked: %s", many)
	}
}

func inf() float64 {
	var z float64
	return 1 / z
}
