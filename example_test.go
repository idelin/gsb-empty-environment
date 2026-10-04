package bitmap_test

import (
	"fmt"

	"bitmap"
)

func Example() {
	// Sparse discrete ids.
	a := bitmap.New()
	a.Add(7)
	a.Add(42)
	// Dense consecutive range.
	a.AddRange(1000, 20000)

	b := bitmap.New()
	b.AddRange(1500, 3000)
	b.Add(42)

	fmt.Println("a:", a.Cardinality())
	fmt.Println("b:", b.Cardinality())
	fmt.Println("AND:", bitmap.And(a, b).Cardinality())
	fmt.Println("OR:", bitmap.Or(a, b).Cardinality())
	fmt.Println("XOR:", bitmap.Xor(a, b).Cardinality())
	fmt.Println("contains 7:", a.Contains(7))

	arrays, bitmaps, runs := a.Stats()
	fmt.Printf("containers: arrays=%d bitmaps=%d runs=%d\n", arrays, bitmaps, runs)

	// Output:
	// a: 19002
	// b: 1501
	// AND: 1501
	// OR: 19002
	// XOR: 17501
	// contains 7: true
	// containers: arrays=0 bitmaps=0 runs=1
}
