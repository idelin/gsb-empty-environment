// 钻石依赖演示：A -> B, A -> C, (B,C) -> D -> Effect
package main

import (
	"fmt"

	"reactive"
)

func main() {
	a := reactive.NewSignal(1)

	var bCalls, cCalls, dCalls int

	b := reactive.NewComputed(func() interface{} {
		bCalls++
		v := a.Get().(int) + 1
		fmt.Printf("  [compute] B = A+1 = %d\n", v)
		return v
	})
	c := reactive.NewComputed(func() interface{} {
		cCalls++
		v := a.Get().(int) * 2
		fmt.Printf("  [compute] C = A*2 = %d\n", v)
		return v
	})
	d := reactive.NewComputed(func() interface{} {
		dCalls++
		bv, cv := b.Get().(int), c.Get().(int)
		consistent := cv == 2*(bv-1) // B、C 是否来自同一版本的 A
		fmt.Printf("  [compute] D = B+C = %d+%d = %d  (consistent=%v)\n", bv, cv, bv+cv, consistent)
		return bv + cv
	})
	reactive.NewEffect(func() {
		fmt.Printf("  [effect]  D = %d\n", d.Get().(int))
	})

	fmt.Println("--- initial ---")
	fmt.Printf("counts: B=%d C=%d D=%d\n", bCalls, cCalls, dCalls)

	fmt.Println("--- A.Set(2) ---")
	a.Set(2)
	fmt.Printf("counts: B=%d C=%d D=%d  (each must be 2: initial + exactly one update)\n", bCalls, cCalls, dCalls)

	fmt.Println("--- A.Set(3) ---")
	a.Set(3)
	fmt.Printf("counts: B=%d C=%d D=%d\n", bCalls, cCalls, dCalls)
	fmt.Printf("final: A=%d B=%d C=%d D=%d\n",
		a.Get().(int), b.Get().(int), c.Get().(int), d.Get().(int))
}
