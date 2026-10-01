// 演示：对文法 A -> 'a' 'b' 'c' | 'a' 'b' 'd' 分别用 K=2 与 K=3 构建
// LL(k) 预测分析器，并用 K=3 的解析器无回溯线性解析输入。
package main

import (
	"errors"
	"fmt"

	"llk/llk"
)

func main() {
	g := llk.NewGrammar("A",
		llk.Production{LHS: "A", RHS: []llk.Symbol{llk.T("a"), llk.T("b"), llk.T("c")}},
		llk.Production{LHS: "A", RHS: []llk.Symbol{llk.T("a"), llk.T("b"), llk.T("d")}},
	)

	fmt.Println("== K=2：公共前缀 \"a b\" 无法区分，期望文法超界异常 ==")
	if _, err := llk.BuildTable(g, 2); err != nil {
		var llkErr *llk.LLKError
		if errors.As(err, &llkErr) {
			fmt.Println("LLKError:", llkErr)
		}
	}

	fmt.Println("\n== K=3：预测表构建成功 ==")
	table, err := llk.BuildTable(g, 3)
	if err != nil {
		panic(err)
	}
	fmt.Print(table.String())

	fmt.Println("\n== 无回溯线性解析 ==")
	for _, input := range [][]string{
		{"a", "b", "c"},
		{"a", "b", "d"},
		{"a", "b", "x"},
	} {
		p := llk.NewParser(table)
		err := p.Parse(input)
		if err != nil {
			fmt.Printf("  parse %v -> 拒绝：%v\n", input, err)
		} else {
			fmt.Printf("  parse %v -> 接受（预测表查询 %d 次，消费 %d 个 Token，无回溯）\n",
				input, p.Steps, len(input))
		}
	}
}
