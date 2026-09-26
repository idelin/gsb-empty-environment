package main

import (
	"fmt"

	"wavelet"
)

func main() {
	data := []int{7, -2, 7, 3, -2, 9, 0}
	tr := wavelet.New(data)

	fmt.Println("data:", data)
	fmt.Println("len:", tr.Len())
	fmt.Println("access(0..6):", func() []int {
		out := make([]int, tr.Len())
		for i := range out {
			out[i] = tr.Access(i)
		}
		return out
	}())

	kth, err := tr.KthSmallest(1, 6, 2)
	fmt.Printf("kth smallest in [1,6), k=2: %d (err=%v)\n", kth, err)

	fmt.Println("freq of 7 in [0,7):", tr.Freq(0, 7, 7))
	fmt.Println("count of values in [-2,3] within [0,7):", tr.RangeFreq(0, 7, -2, 3))

	if _, err := tr.KthSmallest(4, 4, 0); err != nil {
		fmt.Println("empty range rejected:", err)
	}

	fmt.Println("layout:", tr.Layout())
	fmt.Println("bytes:", tr.Bytes())
}
