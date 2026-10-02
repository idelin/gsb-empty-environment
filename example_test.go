package roaring

import "fmt"

func Example() {
	a := New()
	a.Add(7)
	a.AddRange(100, 200) // [100, 200)

	b := New()
	b.AddRange(150, 300)

	fmt.Println(a.And(b).Cardinality()) // [150, 200)
	fmt.Println(a.Or(b).Cardinality())  // {7} + [100, 300)
	fmt.Println(a.Xor(b).Cardinality()) // {7} + [100, 150) + [200, 300)

	// Persist and restore with zero copies.
	buf := a.Serialize()
	restored, err := Deserialize(buf)
	if err != nil {
		panic(err)
	}
	fmt.Println(restored.Equals(a))
	// Output:
	// 50
	// 201
	// 151
	// true
}
