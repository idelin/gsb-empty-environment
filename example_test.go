package reactive_test

import (
	"fmt"

	"reactive"
)

func Example() {
	s := reactive.NewScope()
	x := reactive.NewSignal(s, 0)
	y := reactive.NewSignal(s, 0)

	reactive.NewEffect(s, func() {
		fmt.Printf("effect: x=%d y=%d\n", x.Get(), y.Get())
	})

	s.Batch(func() {
		x.Set(10) // silent
		y.Set(20) // silent
	})

	// Output:
	// effect: x=0 y=0
	// effect: x=10 y=20
}
