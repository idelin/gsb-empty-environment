// 钻石依赖演示：A -> B, A -> C, (B, C) -> D
// 更新 A，观察 B/C/D 的重算次数与 D 读到的一致性快照。
package main

import (
	"fmt"

	"reactive"
)

func main() {
	a := reactive.NewSignal(1)

	var bRuns, cRuns, dRuns int
	b := reactive.NewComputed(func() interface{} {
		bRuns++
		v := a.Get().(int) + 1
		fmt.Printf("  [recompute] B = A+1 = %d\n", v)
		return v
	})
	c := reactive.NewComputed(func() interface{} {
		cRuns++
		v := a.Get().(int) * 2
		fmt.Printf("  [recompute] C = A*2 = %d\n", v)
		return v
	})
	d := reactive.NewComputed(func() interface{} {
		dRuns++
		bv, cv := b.Get().(int), c.Get().(int)
		fmt.Printf("  [recompute] D = B+C = %d+%d = %d  (读到 B、C 均为最新版本)\n", bv, cv, bv+cv)
		return bv + cv
	})

	fmt.Println("== 初始化后 ==")
	fmt.Printf("B=%v C=%v D=%v  runs: B=%d C=%d D=%d\n\n", b.Get(), c.Get(), d.Get(), bRuns, cRuns, dRuns)

	fmt.Println("== A.Set(10)：标脏 -> 拓扑序刷新 ==")
	a.Set(10)
	fmt.Printf("B=%v C=%v D=%v\n", b.Get(), c.Get(), d.Get())
	fmt.Printf("本轮重算次数: B=%d C=%d D=%d (D 仅 1 次，无中间瞬态)\n\n", bRuns-1, cRuns-1, dRuns-1)

	fmt.Println("== A.Set(5) ==")
	a.Set(5)
	fmt.Printf("B=%v C=%v D=%v\n", b.Get(), c.Get(), d.Get())
	fmt.Printf("累计重算次数: B=%d C=%d D=%d\n", bRuns, cRuns, dRuns)
}
