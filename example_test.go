package windowperiod_test

import (
	"fmt"

	"windowperiod"
)

func Example() {
	win := windowperiod.New(6)
	for _, b := range []byte("abcabcabc") {
		win.Append(b)
	}
	period, repeats, ok := win.Period()
	fmt.Printf("%q x%d (ok=%v)\n", period, repeats, ok)

	win.Undo() // revert the last append; window becomes "cabcab"
	for _, b := range []byte("xyxyxy") {
		win.Append(b)
	}
	period, repeats, ok = win.Period()
	fmt.Printf("%q x%d (ok=%v)\n", period, repeats, ok)
	// Output:
	// "abc" x2 (ok=true)
	// "xy" x3 (ok=true)
}
