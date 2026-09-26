package main

import (
	"fmt"

	"multiset"
)

func main() {
	m := multiset.New()
	must(m.ApplyBatch([]multiset.Delta{{Value: 1, Delta: 2}, {Value: 3, Delta: 1}}))
	fmt.Printf("集合 [1,1,3]：Len=%d Distinct=%d\n", m.Len(), m.Distinct())
	for k := uint64(1); k <= m.Len(); k++ {
		v, _ := m.Select(k)
		fmt.Printf("  Select(%d) = %d\n", k, v)
	}

	// 批量加减（事务）。
	must(m.ApplyBatch([]multiset.Delta{{Value: 1, Delta: -1}, {Value: 2, Delta: 4}, {Value: 3, Delta: -1}}))
	fmt.Printf("批量更新后：Count(1)=%d Count(2)=%d Count(3)=%d Len=%d\n",
		m.Count(1), m.Count(2), m.Count(3), m.Len())

	// 中途一项为负 → 整批回滚。
	err := m.ApplyBatch([]multiset.Delta{{Value: 2, Delta: 1}, {Value: 1, Delta: -5}})
	fmt.Printf("含负数的批次：err=%v；回滚后 Count(1)=%d Count(2)=%d（未变）\n",
		err, m.Count(1), m.Count(2))

	// 相交取最小重数。
	other := multiset.New()
	must(other.ApplyBatch([]multiset.Delta{{Value: 1, Delta: 5}, {Value: 2, Delta: 3}}))
	inter := m.Intersect(other)
	fmt.Printf("相交：Count(1)=%d Count(2)=%d Len=%d\n",
		inter.Count(1), inter.Count(2), inter.Len())

	// 真实编码布局与占用字节。
	enc := m.Encode()
	fmt.Printf("编码布局：magic(4B) + N + 值delta + 重数 + 前缀delta（均 uvarint）\n")
	fmt.Printf("  原始字节: % x\n", enc)
	fmt.Printf("  占用 %d 字节（distinct=%d）\n", len(enc), m.Distinct())
	back, err := multiset.Decode(enc)
	if err != nil {
		panic(err)
	}
	fmt.Printf("  解码往返一致: %v\n", back.Len() == m.Len() && back.Distinct() == m.Distinct())
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}
