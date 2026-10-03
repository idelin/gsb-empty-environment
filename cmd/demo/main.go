// Demo: compressed bitmap with multi-node concurrency arbitration.
package main

import (
	"fmt"
	"sync"
	"time"

	"bitmap/bitmap"
)

func main() {
	fmt.Println("=== 1. 容器自动选择：稀疏ID + 密集区间 ===")
	bm := bitmap.New()
	// 稀疏离散 ID
	for _, id := range []uint64{3, 999, 123456, 1 << 20, 1 << 32, 1<<44 + 777} {
		bm.Add(id)
	}
	// 密集连续区间
	bm.AddRange(1<<22, 1<<22+99999)      // 10万连续ID
	bm.AddRange(5<<32, 5<<32+65535*3+10) // 跨3个容器的连续区间
	// 稠密但无连续段 -> bitmap 容器
	for i := 0; i < 5000; i++ {
		bm.Add(uint64(i * 12)) // 全部落在容器 key=0
	}
	st := bm.Stats()
	fmt.Printf("cardinality=%d containers=%d (array=%d bitmap=%d rle=%d)\n",
		st.Cardinality, st.Containers, st.ArrayContainers, st.BitmapContainers, st.RunContainers)

	fmt.Println("\n=== 2. 多节点并发竞争：16个执行体同时写入 ===")
	shared := bitmap.New()
	const nodes = 16
	const perNode = 100000
	start := time.Now()
	var wg sync.WaitGroup
	for n := 0; n < nodes; n++ {
		wg.Add(1)
		go func(node int) {
			defer wg.Done()
			nd := shared.Node(node)
			// 每个节点写不相交的区段 + 所有节点竞争同一批热点ID
			base := uint64(node * perNode)
			for i := 0; i < perNode; i++ {
				nd.Add(base + uint64(i))
			}
			for i := 0; i < 1000; i++ {
				nd.Add(uint64(1<<40 + i)) // 热点冲突区
			}
		}(n)
	}
	wg.Wait()
	elapsed := time.Since(start)
	want := uint64(nodes*perNode + 1000)
	fmt.Printf("写入 %d 个ID（含1000个全节点竞争的热点ID），耗时 %v\n", nodes*perNode+1000*nodes, elapsed)
	fmt.Printf("cardinality=%d (期望 %d，严格相等 => 无丢失、无重复提交)\n", shared.Cardinality(), want)

	fmt.Println("\n=== 3. 批量原子提交（线性一致性） ===")
	nd := shared.Node(99)
	b := shared.Batch()
	for i := 0; i < 4; i++ {
		b.Add(uint64(1<<48 + i))
	}
	nd.Commit(b)
	fmt.Printf("ContainsAll(批量4个ID)=%v CountPresent=%d\n",
		shared.ContainsAll(uint64(1<<48), uint64(1<<48+1), uint64(1<<48+2), uint64(1<<48+3)),
		shared.CountPresent(uint64(1<<48), uint64(1<<48+1), uint64(1<<48+2), uint64(1<<48+3)))

	fmt.Println("\n=== 4. 百亿级数据模拟：两个Bitmap的交/并/异或 ===")
	const total = uint64(10000000000) // 10^10
	a := bitmap.New()
	a.AddRange(0, total-1) // [0, 100亿)
	c := bitmap.New()
	c.AddRange(total/2, total+total/2-1) // [50亿, 150亿)
	fmt.Printf("A: %d 个元素, %d 个RLE容器; B: %d 个元素, %d 个RLE容器\n",
		a.Cardinality(), a.Stats().Containers, c.Cardinality(), c.Stats().Containers)

	t0 := time.Now()
	inter := a.And(c)
	dAnd := time.Since(t0)
	t0 = time.Now()
	union := a.Or(c)
	dOr := time.Since(t0)
	t0 = time.Now()
	diff := a.Xor(c)
	dXor := time.Since(t0)
	fmt.Printf("AND: |A∩B|=%d  耗时 %v (平均每容器 %v)\n", inter.Cardinality(), dAnd, dAnd/time.Duration(a.Stats().Containers))
	fmt.Printf("OR : |A∪B|=%d  耗时 %v\n", union.Cardinality(), dOr)
	fmt.Printf("XOR: |A△B|=%d  耗时 %v\n", diff.Cardinality(), dXor)

	fmt.Println("\n=== 5. 稀疏Bitmap交并（100万离散ID × 2） ===")
	s1, s2 := bitmap.New(), bitmap.New()
	for i := uint64(0); i < 1000000; i++ {
		s1.Add((i * 7919) % (1 << 32))
		s2.Add((i*104729 + 5000) % (1 << 32))
	}
	t0 = time.Now()
	r1 := s1.And(s2)
	d1 := time.Since(t0)
	t0 = time.Now()
	r2 := s1.Or(s2)
	d2 := time.Since(t0)
	fmt.Printf("AND: |S1∩S2|=%d 耗时 %v;  OR: |S1∪S2|=%d 耗时 %v\n",
		r1.Cardinality(), d1, r2.Cardinality(), d2)
}
