// 运行样例：演示 LL(k) 预测分析器在 K=2 超界、K=3 成功解析的完整过程。
package main

import (
	"errors"
	"fmt"

	"llk"
)

func main() {
	// 验收文法：A -> 'a' 'b' 'c' | 'a' 'b' 'd'
	grammar := &llk.Grammar{
		Start: "A",
		Rules: []*llk.Rule{
			{Name: "A", Alts: []llk.Alternative{
				{llk.T("a"), llk.T("b"), llk.T("c")},
				{llk.T("a"), llk.T("b"), llk.T("d")},
			}},
		},
	}

	fmt.Println("文法: A -> 'a' 'b' 'c' | 'a' 'b' 'd'")
	fmt.Println()

	fmt.Println("== 尝试 K=2 ==")
	_, err := llk.NewParser(grammar, 2)
	var oob *llk.GrammarOutOfBoundsError
	switch {
	case errors.As(err, &oob):
		fmt.Println("构建失败（符合预期）:", err)
	case err != nil:
		fmt.Println("构建失败（意外错误）:", err)
	default:
		fmt.Println("构建成功（不符合预期！）")
	}
	fmt.Println()

	fmt.Println("== 尝试 K=3 ==")
	p, err := llk.NewParser(grammar, 3)
	if err != nil {
		fmt.Println("构建失败:", err)
		return
	}
	fmt.Println("构建成功，分支预测表:")
	for la, alt := range p.Table("A") {
		fmt.Printf("  前瞻 %-8q -> 候选式 #%d\n", la, alt)
	}
	fmt.Println()

	for _, input := range [][]string{
		{"a", "b", "c"},
		{"a", "b", "d"},
		{"a", "b", "x"},
	} {
		fmt.Printf("解析输入 %v: ", input)
		tree, err := p.Parse(input)
		if err != nil {
			fmt.Println("失败 ->", err)
			continue
		}
		fmt.Println("成功 ->", tree)
	}
}
