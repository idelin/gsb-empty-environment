package linearleak_test

import (
	"fmt"

	"example.org/netsec/q0436/linearleak"
)

func ExampleAnalyze() {
	// 两个秘密位的计数查询：x0 + x1 = 0。
	// 实数域上二者都自由，但 0/1 约束把它们都钉死为 0。
	eqs := []linearleak.Equation{
		{Coeff: []int64{1, 1}, RHS: 0, Tag: "count-query-7"},
	}
	rep, err := linearleak.Analyze(2, eqs, nil, linearleak.Budget{})
	if err != nil {
		panic(err)
	}

	fmt.Println(rep.Status)
	for _, f := range rep.Findings {
		fmt.Printf("bit %d forced to %d via %v\n", f.Bit, f.Value, f.Tags)
	}
	// Output:
	// leak
	// bit 0 forced to 0 via [count-query-7]
	// bit 1 forced to 0 via [count-query-7]
}

func ExampleAnalyze_publicBit() {
	// x0 已公开为 1，观测 x0 + x1 = 1 后，x1 被唯一确定为 0。
	eqs := []linearleak.Equation{{Coeff: []int64{1, 1}, RHS: 1}}
	pub := []linearleak.BitValue{{Index: 0, Value: 1}}
	rep, err := linearleak.Analyze(2, eqs, pub, linearleak.Budget{})
	if err != nil {
		panic(err)
	}

	fmt.Println(rep.Status)
	fmt.Println(rep.Findings[0].Bit, rep.Findings[0].Value)
	// Output:
	// leak
	// 1 0
}

func ExampleAnalyze_budgetUnknown() {
	// 极小节点预算下无法完成可行性判定时，结论降级为 unknown。
	n := 20
	row := make([]int64, n)
	row[0], row[1] = 1, 1
	eqs := []linearleak.Equation{{Coeff: row, RHS: 1}}
	rep, err := linearleak.Analyze(n, eqs, nil, linearleak.Budget{FeasibilityNodes: 1, ProofChecks: 3})
	if err != nil {
		panic(err)
	}
	fmt.Println(rep.Status)
	// Output:
	// unknown
}
