// demo 演示 Roaring Bitmap 的稀疏/稠密自适应与 AND/OR 运算。
package main

import (
	"fmt"

	"roaring"
)

func main() {
	// 1. 稀疏插入：保持 Array Chunk
	a := roaring.New()
	for _, v := range []uint32{1, 2, 3} {
		a.Add(v)
	}
	fmt.Printf("插入 [1,2,3]            -> chunk0 类型: %s, 基数: %d\n", a.ChunkType(0), a.Cardinality())

	// 2. 稠密插入：超过 4096 阈值，自动升级为 Bitset Chunk
	a.AddRange(10000, 15000)
	fmt.Printf("再插入 [10000..15000]   -> chunk0 类型: %s, 基数: %d\n", a.ChunkType(0), a.Cardinality())

	// 3. 构造第二个位图，与 a 在 chunk0 上仅有 2 个重叠值
	b := roaring.New()
	b.AddRange(14999, 16000)
	for _, v := range []uint32{1, 2, 3} {
		b.Add(v)
	}

	// 4. AND：交集基数回落到阈值以下，Bitset 自动收缩为 Array
	and := roaring.And(a, b)
	fmt.Printf("AND 结果: %v (chunk0 类型: %s, 基数: %d)\n", and.ToSlice(), and.ChunkType(0), and.Cardinality())

	// 5. OR：并集保持稠密，仍为 Bitset
	or := roaring.Or(a, b)
	fmt.Printf("OR  基数: %d (chunk0 类型: %s), 含 1=%v, 含 16000=%v, 含 9999=%v\n",
		or.Cardinality(), or.ChunkType(0), or.Contains(1), or.Contains(16000), or.Contains(9999))
}
