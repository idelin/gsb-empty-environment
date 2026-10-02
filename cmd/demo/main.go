// 运行样例：演示 Roaring Bitmap 的稀疏/稠密自适应与交集、并集运算。
package main

import (
	"fmt"

	"roaring"
)

func showChunks(name string, b *roaring.Bitmap) {
	fmt.Printf("%s: cardinality=%d, chunks=%d\n", name, b.Cardinality(), len(b.ChunkTypes()))
	for _, info := range b.ChunkTypes() {
		fmt.Printf("  chunk[key=%-5d] type=%-6s cardinality=%d\n", info.Key, info.Type, info.Cardinality)
	}
}

func main() {
	// 1. 稀疏插入 [1, 2, 3]：保持 Array Chunk。
	a := roaring.New()
	a.Add(1)
	a.Add(2)
	a.Add(3)
	fmt.Println("== 插入 [1, 2, 3] ==")
	showChunks("A", a)

	// 2. 稠密插入 [10000..15000]：触发 Array -> Bitset 转换。
	a.AddRange(10000, 15000)
	fmt.Println("\n== A 继续插入 [10000..15000]（5001 个值，越过 4096 阈值）==")
	showChunks("A", a)

	// 3. 构造第二个 Bitmap 并执行 AND / OR。
	b := roaring.New()
	b.Add(2)
	b.Add(3)
	b.Add(4)
	b.AddRange(12000, 16000)
	fmt.Println("\n== Bitmap B ==")
	showChunks("B", b)

	and := roaring.And(a, b)
	fmt.Println("\n== A AND B = {2,3} ∪ [12000..15000]，交集基数回落，Bitset 收缩为 Array ==")
	showChunks("A AND B", and)
	arr := and.ToArray()
	fmt.Printf("  结果前 5 个: %v, 后 3 个: %v\n", arr[:5], arr[len(arr)-3:])

	or := roaring.Or(a, b)
	fmt.Println("\n== A OR B = {1,2,3,4} ∪ [10000..16000]，稠密并集保持 Bitset ==")
	showChunks("A OR B", or)
}
