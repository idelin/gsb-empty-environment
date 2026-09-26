package main

import (
	"fmt"

	"omset"
)

func main() {
	m := multiset.FromSlice([]int64{1, 1, 3})
	fmt.Println("elements:", m.Elements(), "total:", m.Len(), "distinct:", m.Distinct())

	v, _ := m.Select(2)
	fmt.Println("Select(2) =", v, "// [1,1,3] 第 2 个是 1")

	enc := m.Encode()
	fmt.Printf("encoded layout: % x (%d bytes, SizeBytes()=%d)\n", enc, len(enc), m.SizeBytes())

	other := multiset.FromSlice([]int64{1, 3, 3})
	fmt.Println("intersect [1,1,3] ∩ [1,3,3] =", m.Intersect(other).Elements(), "// 取最小重数")

	err := m.Apply(
		multiset.Delta{Value: 5, Diff: 2},
		multiset.Delta{Value: 3, Diff: -2}, // 3 只有 1 个 -> 整批回滚
	)
	fmt.Println("batch with negative result:", err)
	fmt.Println("after rollback, elements:", m.Elements(), "// 状态未变")

	if err := m.Apply(
		multiset.Delta{Value: 5, Diff: 2},
		multiset.Delta{Value: 1, Diff: -1},
	); err == nil {
		fmt.Println("valid batch committed, elements:", m.Elements())
		fmt.Println("Rank(3) =", m.Rank(3), "// <=3 的累计重数")
	}
}
