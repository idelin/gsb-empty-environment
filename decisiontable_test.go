package decisiontable

import (
	"math/rand"
	"strings"
	"testing"
)

// businessTable 构造验收用的 10 条业务线折扣规则（规则3 与 规则7 冲突）。
func businessTable() *Table {
	t := NewTable(
		Dimension{Name: "VIP", Kind: BoolKind},
		Dimension{Name: "金额", Kind: NumberKind},
		Dimension{Name: "渠道", Kind: EnumKind},
	)
	t.Add("0.95", Bool(true), Le(100), Any())                   // 规则1
	t.Add("0.9", Bool(true), Between(100, 500), Any())          // 规则2
	t.Add("0.8", Bool(true), Gt(500), Any())                    // 规则3
	t.Add("1.0", Bool(false), Le(100), Any())                   // 规则4
	t.Add("0.95", Bool(false), Between(100, 500), Any())        // 规则5
	t.Add("0.9", Bool(false), Gt(500), Any())                   // 规则6
	t.Add("0.7", Bool(true), Gt(500), Any())                    // 规则7 ⚠ 与规则3冲突
	t.Add("0.95", Bool(true), Le(100), Eq("online"))            // 规则8
	t.Add("0.95", Bool(false), Between(100, 500), Eq("mobile")) // 规则9
	t.Add("0.9", Bool(true), Between(100, 500), Eq("online"))   // 规则10
	return t
}

// fixedTable 返回修复冲突后的表（规则7 动作 0.7 -> 0.8）。
func fixedTable() *Table {
	t := businessTable()
	t.SetAction(7, "0.8")
	return t
}

func TestConflicts_FindsRule3VsRule7(t *testing.T) {
	tb := businessTable()
	cs := tb.Conflicts()
	if len(cs) != 1 {
		t.Fatalf("期望恰好 1 处冲突, 实际 %d 处: %v", len(cs), cs)
	}
	c := cs[0]
	if c.A.ID != 3 || c.B.ID != 7 {
		t.Errorf("冲突行定位错误: 期望 规则3 vs 规则7, 实际 规则%d vs 规则%d", c.A.ID, c.B.ID)
	}
	if c.A.Action != "0.8" || c.B.Action != "0.7" {
		t.Errorf("冲突动作错误: 期望 0.8 vs 0.7, 实际 %s vs %s", c.A.Action, c.B.Action)
	}
	// 重叠区域: VIP=true, 金额=(500,+∞), 渠道=通配
	if !c.Region[0].Bools[true] || len(c.Region[0].Bools) != 1 {
		t.Errorf("重叠区域 VIP 维度错误: %+v", c.Region[0])
	}
	iv := c.Region[1].Num
	if iv.Lo != 500 || !iv.LoOpen || !iv.HiInf {
		t.Errorf("重叠区域 金额维度错误: %+v", iv)
	}
	if !c.Region[2].Any {
		t.Errorf("重叠区域 渠道维度应为通配: %+v", c.Region[2])
	}
	msg := c.String(tb.Dims)
	if !strings.Contains(msg, "VIP=true") || !strings.Contains(msg, "金额>500") {
		t.Errorf("冲突描述缺少关键信息: %s", msg)
	}
	t.Logf("冲突定位: %s", msg)
}

func TestConflicts_AfterFix(t *testing.T) {
	tb := fixedTable()
	if n := len(tb.Conflicts()); n != 0 {
		t.Fatalf("修复后仍有 %d 处冲突", n)
	}
	// 另一种修复方式: 删除规则7
	tb2 := businessTable()
	tb2.RemoveRule(7)
	if n := len(tb2.Conflicts()); n != 0 {
		t.Fatalf("删除规则7后仍有 %d 处冲突", n)
	}
}

func TestCompress(t *testing.T) {
	tb := fixedTable()
	rules := tb.Compress()
	if len(rules) != 6 {
		t.Fatalf("压缩后规则数: 期望 6, 实际 %d", len(rules))
	}
	// 压缩结果不得再有冲突
	ct := &Table{Dims: tb.Dims, Rules: rules}
	if n := len(ct.Conflicts()); n != 0 {
		t.Fatalf("压缩结果仍存在 %d 处冲突", n)
	}
	// 冗余细化规则应被吸收为通配: VIP=true 且 金额≤100 且 渠道=* => 0.95
	found := false
	for _, r := range rules {
		if r.Action == "0.95" && !r.Conds[0].Any && r.Conds[0].Bools[true] &&
			!r.Conds[1].Any && r.Conds[1].Num.Hi == 100 && r.Conds[2].Any {
			found = true
		}
	}
	if !found {
		t.Error("规则8 未被吸收进 VIP=true 且 金额≤100 且 渠道=* 的通配规则")
	}
	// 压缩前后语义等价（随机输入对比首匹配动作）
	rng := rand.New(rand.NewSource(42))
	for i := 0; i < 2000; i++ {
		in := randomInput(rng)
		before, ok1 := tb.Match(in)
		after, ok2 := ct.Match(in)
		if ok1 != ok2 || before != after {
			t.Fatalf("压缩前后语义不一致: input=%v before=(%s,%v) after=(%s,%v)", in, before, ok1, after, ok2)
		}
	}
}

func TestCompress_Idempotent(t *testing.T) {
	tb := fixedTable()
	once := tb.Compress()
	twice := (&Table{Dims: tb.Dims, Rules: once}).Compress()
	if len(once) != len(twice) {
		t.Fatalf("压缩不满足幂等: %d -> %d", len(once), len(twice))
	}
}

func TestMerge_BoolWildcard(t *testing.T) {
	tb := NewTable(Dimension{Name: "VIP", Kind: BoolKind})
	tb.Add("0.9", Bool(true))
	tb.Add("0.9", Bool(false))
	rules := tb.Compress()
	if len(rules) != 1 || !rules[0].Conds[0].Any {
		t.Fatalf("布尔维度未合并为通配: %+v", rules)
	}
}

func TestMerge_NumberAdjacent(t *testing.T) {
	tb := NewTable(Dimension{Name: "金额", Kind: NumberKind})
	tb.Add("0.9", Le(100))
	tb.Add("0.9", Between(100, 500))
	tb.Add("0.9", Gt(500))
	rules := tb.Compress()
	if len(rules) != 1 || !rules[0].Conds[0].Any {
		t.Fatalf("相接区间未合并为通配: %+v", rules)
	}
	// 有空隙的区间不得合并
	tb2 := NewTable(Dimension{Name: "金额", Kind: NumberKind})
	tb2.Add("0.9", Lt(100))
	tb2.Add("0.9", Gt(500))
	if n := len(tb2.Compress()); n != 2 {
		t.Fatalf("有空隙的区间被错误合并, 剩 %d 条", n)
	}
}

func TestInterval_Basics(t *testing.T) {
	a := Gt(500).Num // (500, +∞)
	b := Le(100).Num // (-∞, 100]
	if _, ok := a.intersect(b); ok {
		t.Error("(500,+∞) 与 (-∞,100] 不应相交")
	}
	c := Between(100, 500).Num // (100, 500]
	if _, ok := a.intersect(c); ok {
		t.Error("(500,+∞) 与 (100,500] 不应相交 (端点 500 互斥)")
	}
	m, ok := a.merge(c)
	if !ok || !m.LoInf && (m.Lo != 100 || !m.LoOpen) || !m.HiInf {
		t.Errorf("(100,500] 与 (500,+∞) 应合并为 (100,+∞), 得到 %+v ok=%v", m, ok)
	}
	if !Gt(500).Num.contains(501) || Gt(500).Num.contains(500) {
		t.Error("contains 边界判定错误")
	}
}

func TestTree_Evaluate(t *testing.T) {
	tb := fixedTable()
	compressed := tb.Compress()
	tree := BuildTree(tb.Dims, compressed)
	if n := tree.LeafCount(); n != 6 {
		t.Fatalf("判定树叶子数: 期望 6, 实际 %d", n)
	}
	cases := []struct {
		in   Input
		want string
	}{
		{Input{"VIP": true, "金额": 600.0}, "0.8"},
		{Input{"VIP": true, "金额": 500.0}, "0.9"},    // 边界: 500 属于 (100,500]
		{Input{"VIP": true, "金额": 500.0001}, "0.8"}, // 边界: 刚好超过 500
		{Input{"VIP": true, "金额": 100.0}, "0.95"},   // 边界: 100 属于 ≤100
		{Input{"VIP": true, "金额": 100.5}, "0.9"},
		{Input{"VIP": true, "金额": 80.0, "渠道": "online"}, "0.95"},
		{Input{"VIP": false, "金额": 50.0}, "1.0"},
		{Input{"VIP": false, "金额": 300.0, "渠道": "mobile"}, "0.95"},
		{Input{"VIP": false, "金额": 999.0}, "0.9"},
	}
	for _, tc := range cases {
		got, ok := tree.Evaluate(tb.Dims, tc.in)
		if !ok || got != tc.want {
			t.Errorf("Evaluate(%v) = (%s,%v), 期望 (%s,true)", tc.in, got, ok, tc.want)
		}
	}
	// 随机输入: 判定树与线性扫描结果一致
	rng := rand.New(rand.NewSource(7))
	ct := &Table{Dims: tb.Dims, Rules: compressed}
	for i := 0; i < 2000; i++ {
		in := randomInput(rng)
		fast, ok1 := tree.Evaluate(tb.Dims, in)
		slow, ok2 := ct.Match(in)
		if ok1 != ok2 || fast != slow {
			t.Fatalf("判定树与线性扫描不一致: input=%v tree=(%s,%v) scan=(%s,%v)", in, fast, ok1, slow, ok2)
		}
	}
}

func TestTree_EnumBranch(t *testing.T) {
	tb := NewTable(
		Dimension{Name: "渠道", Kind: EnumKind},
		Dimension{Name: "VIP", Kind: BoolKind},
	)
	tb.Add("A", Eq("online"), Bool(true))
	tb.Add("B", Eq("mobile"), Any())
	tb.Add("C", Any(), Bool(false))
	tree := BuildTree(tb.Dims, tb.Rules)
	cases := []struct {
		in   Input
		want string
	}{
		{Input{"渠道": "online", "VIP": true}, "A"},
		{Input{"渠道": "mobile", "VIP": true}, "B"},
		{Input{"渠道": "mobile", "VIP": false}, "B"},
		{Input{"渠道": "offline", "VIP": false}, "C"}, // 默认分支
	}
	for _, tc := range cases {
		got, ok := tree.Evaluate(tb.Dims, tc.in)
		if !ok || got != tc.want {
			t.Errorf("Evaluate(%v) = (%s,%v), 期望 (%s,true)", tc.in, got, ok, tc.want)
		}
	}
}

func randomInput(rng *rand.Rand) Input {
	channels := []string{"online", "mobile", "offline"}
	return Input{
		"VIP": rng.Intn(2) == 0,
		"金额":  float64(rng.Intn(200000)) / 100, // 0 ~ 2000, 含两位小数
		"渠道":  channels[rng.Intn(len(channels))],
	}
}
