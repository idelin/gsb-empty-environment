package netsec_test

import (
	"fmt"

	netsec "example.org/netsec/q0383"
)

// ExampleVerify shows an 8-bit multiply whose overflow check is correctly
// reconstructed after widening the operation to 16 bits.
func ExampleVerify() {
	// Old graph: 8-bit multiply with an explicit overflow error node 7.
	ob := netsec.NewBuilder("old", 2)
	op := ob.Binary(3, netsec.OpMul, ob.Input(0), ob.Input(1))
	oovf := ob.Overflow(4, op)
	oErr, oOk := ob.NewBlock(), ob.NewBlock()
	ob.Branch(oovf, oErr, oOk)
	ob.UseBlock(oErr)
	ob.Error(7)
	ob.UseBlock(oOk)
	oldG := ob.Return(op)

	// New graph: widen to 16 bits, multiply there, reconstruct the 8-bit
	// overflow predicate, truncate the value back to 8 bits.
	nb := netsec.NewBuilder("new", 2)
	x := nb.SExt(1, nb.Input(0), 16)
	y := nb.SExt(2, nb.Input(1), 16)
	wide := nb.Binary(3, netsec.OpMul, x, y)
	low := nb.Trunc(5, wide, 8)
	reext := nb.SExt(7, low, 16)
	novf := nb.Compare(8, netsec.OpNe, wide, reext)
	nErr, nOk := nb.NewBlock(), nb.NewBlock()
	nb.Branch(novf, nErr, nOk)
	nb.UseBlock(nErr)
	nb.Error(7)
	nb.UseBlock(nOk)
	newG := nb.Return(low)

	res, err := netsec.Verify(oldG, newG, netsec.Config{Budget: 1_000_000})
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	fmt.Println(res.Status)
	// Output: equivalent
}
