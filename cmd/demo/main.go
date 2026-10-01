// 稀疏数组演示：语义样例 + 大逻辑长度下的物理占用与更新复杂度证据。
package main

import (
	"fmt"
	"math/rand"
	"time"

	sparse "example.com/sparsearr"
)

func main() {
	fmt.Println("== 1. 基本语义：缺省 0 -> 2，显式 0 不是缺失 ==")
	a := sparse.New(10, 0)
	a.Assign(3, 0) // 显式 0：固定为例外
	a.Assign(5, 7)
	fmt.Printf("变更前: Get(3)=%d Get(4)=%d Get(5)=%d 例外数=%d\n", a.Get(3), a.Get(4), a.Get(5), a.Exceptions())
	a.ChangeDefault(2)
	fmt.Printf("缺省 0->2 后: Get(3)=%d(显式0保留) Get(4)=%d(跟随) Get(5)=%d(例外不跟随) 例外数=%d\n",
		a.Get(3), a.Get(4), a.Get(5), a.Exceptions())
	fmt.Printf("RangeSum(0,10)=%d （含缺省贡献 8*2+0+7=23）\n", a.RangeSum(0, 10))

	fmt.Println("\n== 2. 回收：旧例外等于新缺省可回收，回收后重新跟随 ==")
	a.ChangeDefault(7) // 例外 7 == 新缺省 → 回收
	fmt.Printf("缺省 2->7 后: Get(5)=%d 例外数=%d（7 被回收）\n", a.Get(5), a.Exceptions())
	a.ChangeDefault(1)
	fmt.Printf("缺省 7->1 后: Get(5)=%d（回收位置重新跟随）Get(3)=%d（显式0仍固定）\n", a.Get(5), a.Get(3))

	fmt.Println("\n== 3. 惰性层：范围加法与点赋值混合 ==")
	b := sparse.New(100, 0)
	b.RangeAdd(0, 50, 3)
	b.Assign(10, 100)
	b.RangeAdd(0, 50, 1) // 赋值点也被推动
	fmt.Printf("Get(10)=%d Get(20)=%d Get(60)=%d RangeSum(0,50)=%d\n",
		b.Get(10), b.Get(20), b.Get(60), b.RangeSum(0, 50))

	fmt.Println("\n== 4. 快照：保留位置身份与历史状态 ==")
	snap := b.Snapshot()
	b.RangeAdd(0, 100, 10)
	b.ChangeDefault(5)
	fmt.Printf("旧快照: Get(10)=%d RangeSum(0,50)=%d | 新头: Get(10)=%d RangeSum(0,50)=%d\n",
		snap.Get(10), snap.RangeSum(0, 50), b.Get(10), b.RangeSum(0, 50))

	fmt.Println("\n== 5. 累计定位（仅非负快照开放） ==")
	c := sparse.New(10, 0)
	c.Assign(2, 5)
	c.Assign(5, 3)
	for _, cum := range []int64{1, 5, 6, 8} {
		p, ok := c.Locate(cum)
		fmt.Printf("Locate(%d) -> 下标 %d (ok=%v)\n", cum, p, ok)
	}
	c.Assign(1, -1)
	_, ok := c.Locate(1)
	fmt.Printf("引入负值后 NonNeg=%v, Locate 开放=%v\n", c.NonNeg(), ok)

	fmt.Println("\n== 6. 大逻辑长度：物理占用与更新复杂度证据 ==")
	const n = uint64(1) << 40 // 约 1.1e12
	rng := rand.New(rand.NewSource(7))
	big := sparse.New(n, 0)

	t0 := time.Now()
	for i := 0; i < 20000; i++ {
		big.Assign(rng.Uint64()%n, int64(rng.Intn(1000)))
	}
	fmt.Printf("20000 次 Assign:        %v (%v/op)\n", time.Since(t0), time.Since(t0)/20000)

	t0 = time.Now()
	for i := 0; i < 2000; i++ {
		l := rng.Uint64() % (n - 1)
		r := min(n, l+1+rng.Uint64()%(1<<30))
		big.RangeAdd(l, r, int64(rng.Intn(10)))
	}
	fmt.Printf("2000 次 RangeAdd(宽至1e9): %v (%v/op)\n", time.Since(t0), time.Since(t0)/2000)

	t0 = time.Now()
	var checksum int64
	for i := 0; i < 10000; i++ {
		l := rng.Uint64() % (n - 1)
		r := min(n, l+1+rng.Uint64()%(1<<30))
		checksum += big.RangeSum(l, r)
	}
	fmt.Printf("10000 次 RangeSum(宽至1e9): %v (%v/op) checksum=%d\n", time.Since(t0), time.Since(t0)/10000, checksum)

	t0 = time.Now()
	for i := 0; i < 200; i++ {
		big.Locate(1 + rng.Int63n(1<<40))
	}
	fmt.Printf("200 次 Locate:          %v (%v/op)\n", time.Since(t0), time.Since(t0)/200)

	fmt.Printf("\n逻辑长度 n=2^40=%d，稠密等效 %d 字节\n", n, big.LogicalBytes())
	fmt.Printf("物理占用 %d 字节（例外 %d 个），压缩比 %.2e x\n",
		big.PhysicalBytes(), big.Exceptions(), float64(big.LogicalBytes())/float64(big.PhysicalBytes()))

	fmt.Println("\n== 7. 编码布局（真实字节，非 map 包装） ==")
	d := sparse.New(1<<40, 0)
	d.Assign(1000, 1)
	d.Assign(1001, -2)
	d.Assign(1002, 3)
	d.Compact() // 强制落盘到压缩 run，展示块编码
	fmt.Print(d.LayoutString())
}
