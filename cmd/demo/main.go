// Demo of the sliding-window exact quantile maintainer (window = 10s).
package main

import (
	"fmt"
	"time"

	"slidewin"
)

var t0 = time.Unix(1700000000, 0)

func at(sec float64) time.Time {
	return t0.Add(time.Duration(sec * float64(time.Second)))
}

func show(w *slidewin.Window, label string) {
	if v, ok := w.Quantile(0.5); ok {
		fmt.Printf("%-44s n=%d  q0.5=%v\n", label, w.Len(), v)
	} else {
		fmt.Printf("%-44s n=0  (empty)\n", label)
	}
}

func main() {
	w := slidewin.NewWindow(10 * time.Second)

	fmt.Println("== acceptance scenario: window holds [1 1 9 20] ==")
	var ones []uint64
	for _, v := range []float64{1, 1, 9, 20} {
		id, _ := w.Add(at(0), v)
		if v == 1 {
			ones = append(ones, id)
		}
	}
	show(w, "after adding 1,1,9,20") // q=0.5 -> floor(0.5*3)=1 -> 1

	w.Retract(ones[0])                            // retract a single 1
	show(w, "after retracting one 1 -> [1 9 20]") // q=0.5 -> floor(0.5*2)=1 -> 9

	fmt.Println()
	fmt.Println("== sliding: stream flows in and expires (window=10s) ==")
	w2 := slidewin.NewWindow(10 * time.Second)
	// t=0..9: values arrive, some out of order
	feed := []struct {
		ts float64
		v  float64
	}{
		{0, 50}, {1, 10}, {2, 30}, {3, 20}, {4, 40},
		{5, 90}, {6, 60}, {5.5, 70}, // out-of-order insert at 5.5
		{7, 80}, {8, 100}, {9, 110},
	}
	for _, e := range feed {
		w2.Add(at(e.ts), e.v)
	}
	show(w2, "t=9,  values 10..110 in window")

	w2.Advance(at(12)) // events at t<=2 (50,10,30) expire
	show(w2, "t=12, t<=2 expired")

	w2.Advance(at(15)) // events at t<=5 expire (20,40,90)
	show(w2, "t=15, t<=5 expired")

	w2.Add(at(14.5), 25) // late but still inside (15-10, 15]
	w2.Add(at(4), 999)   // born expired: silently dropped
	show(w2, "t=15, +late 25, +expired 999 dropped")

	w2.Advance(at(26)) // everything drained
	show(w2, "t=26, window drained")
}
