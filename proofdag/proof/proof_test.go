package proof

import (
	"strings"
	"testing"
)

// 构造 (3+4)*(3+4) 的共享图：子推导 3+4 只存一份。
func buildSharedGraph(t *testing.T) (*Graph, ID, ID) {
	t.Helper()
	b := NewBuilder()
	lit3a, err := b.Add("lit", "3")
	if err != nil {
		t.Fatal(err)
	}
	lit3b, err := b.Add("lit", "3")
	if err != nil {
		t.Fatal(err)
	}
	if lit3a != lit3b {
		t.Fatalf("相同公理应共享节点: %d != %d", lit3a, lit3b)
	}
	lit4, err := b.Add("lit", "4")
	if err != nil {
		t.Fatal(err)
	}
	sum1, err := b.Add("add", "7", lit3a, lit4)
	if err != nil {
		t.Fatal(err)
	}
	sum2, err := b.Add("add", "7", lit3b, lit4)
	if err != nil {
		t.Fatal(err)
	}
	if sum1 != sum2 {
		t.Fatalf("结构相同的推导应共享节点: %d != %d", sum1, sum2)
	}
	root, err := b.Add("mul", "49", sum1, sum2)
	if err != nil {
		t.Fatal(err)
	}
	return b.Build(), root, sum1
}

func TestSharingCompressesDAG(t *testing.T) {
	g, root, _ := buildSharedGraph(t)
	// 共享图: lit3, lit4, add7, mul49 共 4 个节点。
	if g.Len() != 4 {
		t.Fatalf("共享图应有 4 个节点，实际 %d", g.Len())
	}
	// 展开成树后: mul(add(3,4), add(3,4)) 共 7 个节点。
	tree, err := Unroll(g, root)
	if err != nil {
		t.Fatal(err)
	}
	if tree.Size() != 7 {
		t.Fatalf("展开树应有 7 个节点，实际 %d", tree.Size())
	}
}

func TestUnrolledTreeMatchesIndependentTree(t *testing.T) {
	g, root, _ := buildSharedGraph(t)
	got, err := Unroll(g, root)
	if err != nil {
		t.Fatal(err)
	}
	// 独立手工构造的展开树。
	want := &Tree{Rule: "mul", Conclusion: "49", Children: []*Tree{
		{Rule: "add", Conclusion: "7", Children: []*Tree{
			{Rule: "lit", Conclusion: "3"},
			{Rule: "lit", Conclusion: "4"},
		}},
		{Rule: "add", Conclusion: "7", Children: []*Tree{
			{Rule: "lit", Conclusion: "3"},
			{Rule: "lit", Conclusion: "4"},
		}},
	}}
	if !got.Equal(want) {
		t.Fatal("展开树与独立构造的树不一致")
	}
	// 前提顺序不同的树不应相等。
	swapped := &Tree{Rule: "add", Conclusion: "7", Children: []*Tree{
		{Rule: "lit", Conclusion: "4"},
		{Rule: "lit", Conclusion: "3"},
	}}
	if got.Children[0].Equal(swapped) {
		t.Fatal("前提顺序不同的树不应相等")
	}
}

func TestSameConclusionDifferentPremisesNotMerged(t *testing.T) {
	b := NewBuilder()
	lit3, _ := b.Add("lit", "3")
	lit4, _ := b.Add("lit", "4")
	lit2, _ := b.Add("lit", "2")
	lit5, _ := b.Add("lit", "5")
	// 两个结论都是 7，但前提不同：必须保留为不同节点。
	a, _ := b.Add("add", "7", lit3, lit4)
	c, _ := b.Add("add", "7", lit2, lit5)
	if a == c {
		t.Fatal("仅结论相同但前提不同的节点被错误合并")
	}
	// 前提顺序也是身份的一部分：add(3,4) 与 add(4,3) 是不同节点。
	d, _ := b.Add("add", "7", lit4, lit3)
	if d == a {
		t.Fatal("前提顺序不同的节点被错误合并")
	}
	// 完全相同的结构仍然共享。
	e, _ := b.Add("add", "7", lit3, lit4)
	if e != a {
		t.Fatal("结构相同的节点未被共享")
	}
}

func TestVerifyValidGraph(t *testing.T) {
	g, _, _ := buildSharedGraph(t)
	if err := Verify(g, DefaultRules()); err != nil {
		t.Fatalf("合法图验证失败: %v", err)
	}
}

func TestVerifyRejectsWrongConclusion(t *testing.T) {
	g := NewGraph([]Node{
		{Rule: "lit", Conclusion: "3"},
		{Rule: "lit", Conclusion: "4"},
		{Rule: "add", Conclusion: "8", Premises: []ID{0, 1}}, // 3+4 != 8
	})
	err := Verify(g, DefaultRules())
	if err == nil || !strings.Contains(err.Error(), "结论") {
		t.Fatalf("应拒绝错误结论，得到: %v", err)
	}
}

func TestVerifyRejectsUnknownRule(t *testing.T) {
	g := NewGraph([]Node{
		{Rule: "cheat", Conclusion: "42"},
	})
	err := Verify(g, DefaultRules())
	if err == nil || !strings.Contains(err.Error(), "未登记") {
		t.Fatalf("应拒绝未登记规则，得到: %v", err)
	}
}

func TestVerifyRejectsDanglingPremise(t *testing.T) {
	g := NewGraph([]Node{
		{Rule: "neg", Conclusion: "-3", Premises: []ID{9}},
	})
	err := Verify(g, DefaultRules())
	if err == nil || !strings.Contains(err.Error(), "不存在") {
		t.Fatalf("应拒绝悬挂前提，得到: %v", err)
	}
}

func TestVerifyRejectsInjectedCycle(t *testing.T) {
	// 环注入：节点 1 与节点 2 互为前提，绕开 Builder 的拓扑保证。
	g := NewGraph([]Node{
		{Rule: "lit", Conclusion: "1"},
		{Rule: "add", Conclusion: "3", Premises: []ID{0, 2}},
		{Rule: "neg", Conclusion: "2", Premises: []ID{1}},
	})
	err := Verify(g, DefaultRules())
	if err == nil || !strings.Contains(err.Error(), "环") {
		t.Fatalf("应检测出环，得到: %v", err)
	}
}

func TestVerifyRejectsSelfLoop(t *testing.T) {
	g := NewGraph([]Node{
		{Rule: "neg", Conclusion: "-1", Premises: []ID{0}},
	})
	err := Verify(g, DefaultRules())
	if err == nil || !strings.Contains(err.Error(), "环") {
		t.Fatalf("应检测出自环，得到: %v", err)
	}
}

func TestBuilderRejectsUnknownPremise(t *testing.T) {
	b := NewBuilder()
	if _, err := b.Add("neg", "-1", ID(5)); err == nil {
		t.Fatal("Builder 应拒绝不存在的前提")
	}
}
