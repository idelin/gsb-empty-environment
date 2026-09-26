package efano_test

import (
	"fmt"

	"efano"
)

func Example() {
	vals := []uint64{3, 5, 5, 5, 8, 21, 21, 34}
	ef, _ := efano.EncodeAuto(vals)

	fmt.Println("At(4):", ef.At(4))
	fmt.Println("LowerBound(6):", ef.LowerBound(6))
	fmt.Println("LowerBound(100):", ef.LowerBound(100)) // past the end
	start, end := ef.EqualRange(5)
	fmt.Printf("EqualRange(5): [%d,%d) count=%d\n", start, end, ef.Count(5))

	st := ef.Stats()
	fmt.Printf("ledger: l=%d high=%db low=%db serialized=%dB raw=%dB\n",
		st.L, st.HighBits, st.LowBits, st.SerializedBytes, uint64(len(vals))*8)

	// Round-trip through the verifiable byte format.
	data := ef.MarshalBinary()
	back, _ := efano.Parse(data)
	fmt.Println("parsed ok, Verify:", back.Verify() == nil)

	// Output:
	// At(4): 8
	// LowerBound(6): 4
	// LowerBound(100): 8
	// EqualRange(5): [1,4) count=3
	// ledger: l=2 high=16b low=16b serialized=52B raw=64B
	// parsed ok, Verify: true
}
