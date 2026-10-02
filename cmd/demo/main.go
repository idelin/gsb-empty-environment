// demo 演示决策表优化器的完整流程：
// 10 条业务线折扣规则 -> 冲突检测（规则3 vs 规则7）-> 修复 -> 压缩 -> 极小判定树。
package main

import (
	"fmt"

	dt "decisiontable"
)

// buildTable 构造 10 条业务线折扣规则。
// 维度: VIP(布尔), 金额(数值), 渠道(枚举)。
func buildTable() *dt.Table {
	t := dt.NewTable(
		dt.Dimension{Name: "VIP", Kind: dt.BoolKind},
		dt.Dimension{Name: "金额", Kind: dt.NumberKind},
		dt.Dimension{Name: "渠道", Kind: dt.EnumKind},
	)
	t.Add("0.95", dt.Bool(true), dt.Le(100), dt.Any())                   // 规则1
	t.Add("0.9", dt.Bool(true), dt.Between(100, 500), dt.Any())          // 规则2
	t.Add("0.8", dt.Bool(true), dt.Gt(500), dt.Any())                    // 规则3
	t.Add("1.0", dt.Bool(false), dt.Le(100), dt.Any())                   // 规则4
	t.Add("0.95", dt.Bool(false), dt.Between(100, 500), dt.Any())        // 规则5
	t.Add("0.9", dt.Bool(false), dt.Gt(500), dt.Any())                   // 规则6
	t.Add("0.7", dt.Bool(true), dt.Gt(500), dt.Any())                    // 规则7 ⚠ 与规则3冲突
	t.Add("0.95", dt.Bool(true), dt.Le(100), dt.Eq("online"))            // 规则8 (规则1的冗余细化)
	t.Add("0.95", dt.Bool(false), dt.Between(100, 500), dt.Eq("mobile")) // 规则9 (规则5的冗余细化)
	t.Add("0.9", dt.Bool(true), dt.Between(100, 500), dt.Eq("online"))   // 规则10 (规则2的冗余细化)
	return t
}

func main() {
	t := buildTable()

	fmt.Println("========== 1. 原始判定表 (10 条业务线规则) ==========")
	for _, r := range t.Rules {
		fmt.Println("  " + r.String(t.Dims))
	}

	fmt.Println("\n========== 2. 冲突检测 ==========")
	conflicts := t.Conflicts()
	if len(conflicts) == 0 {
		fmt.Println("  未发现冲突")
	}
	for _, c := range conflicts {
		fmt.Println("  " + c.String(t.Dims))
	}

	fmt.Println("\n========== 3. 修复冲突 ==========")
	fmt.Println("  业务裁定: VIP=true 且 金额>500 统一执行折扣 0.8，将规则7动作 0.7 -> 0.8")
	t.SetAction(7, "0.8")
	if n := len(t.Conflicts()); n == 0 {
		fmt.Println("  复检: 冲突数 = 0 ✓")
	} else {
		fmt.Printf("  复检: 仍有 %d 处冲突 ✗\n", n)
	}

	fmt.Println("\n========== 4. 压缩合并 (相同结果的相邻网格 -> 通配符规则) ==========")
	compressed := t.Compress()
	fmt.Printf("  规则数: %d -> %d\n", len(t.Rules), len(compressed))
	for _, r := range compressed {
		fmt.Println("  " + r.String(t.Dims))
	}

	fmt.Println("\n========== 5. 压缩后的极小判定树 ==========")
	tree := dt.BuildTree(t.Dims, compressed)
	fmt.Print(tree.String(t.Dims))
	fmt.Printf("  (叶子节点数: %d)\n", tree.LeafCount())

	fmt.Println("\n========== 6. 匹配验证 (判定树 vs 线性扫描) ==========")
	samples := []dt.Input{
		{"VIP": true, "金额": 600.0, "渠道": "online"},
		{"VIP": true, "金额": 500.0},
		{"VIP": true, "金额": 80.0},
		{"VIP": false, "金额": 300.0, "渠道": "mobile"},
		{"VIP": false, "金额": 999.0},
	}
	ref := &dt.Table{Dims: t.Dims, Rules: compressed}
	for _, in := range samples {
		fast, _ := tree.Evaluate(t.Dims, in)
		want, _ := ref.Match(in)
		status := "✓"
		if fast != want {
			status = "✗"
		}
		fmt.Printf("  %-40s 判定树=%s 线性扫描=%s %s\n", fmt.Sprint(in), fast, want, status)
	}
}
