// Demo of the batched reactive system.
package main

import (
	"fmt"

	"reactive"
)

func main() {
	s := reactive.NewScope()

	x := reactive.NewSignal(s, 0)
	y := reactive.NewSignal(s, 0)
	sum := reactive.NewComputed(s, func() int { return x.Get() + y.Get() })

	reactive.NewEffect(s, func() {
		fmt.Printf("[effect] x=%d y=%d sum=%d\n", x.Get(), y.Get(), sum.Get())
	})

	fmt.Println("-- individual sets (no batch): effect fires per set --")
	x.Set(1)
	y.Set(2)

	fmt.Println("-- batch: x=10, y=20 --")
	s.Batch(func() {
		x.Set(10)
		fmt.Println("   (inside batch: effect silent)")
		y.Set(20)
	})
	fmt.Println("-- batch exited: effect fired exactly once with latest values --")
}
