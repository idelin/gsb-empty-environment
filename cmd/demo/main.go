// Command demo exercises the rlebits package and prints the real encoding
// layout and byte occupancy of each structure.
package main

import (
	"fmt"

	"rlebits"
)

func show(label string, b *rlebits.Bits) {
	s := b.String()
	if len(s) > 64 {
		s = s[:32] + "..." + s[len(s)-16:]
	}
	fmt.Printf("%-28s bits=%s (len=%d)\n", label, s, b.Len())
	fmt.Printf("%-28s runs=%d encoded=%dB heap~%dB raw-bits=%dB\n",
		"", b.Runs(), b.EncodedBytes(), b.HeapBytes(), (b.Len()+7)/8)
}

func main() {
	b := rlebits.New("0001100101111000")
	show("build", b)
	fmt.Printf("%-28s encoded bytes(hex)=% x\n", "layout: first|nruns|lens", b.Encode())

	b.Flip(3, 12)
	show("flip [3,12)", b)
	b.Flip(3, 12)
	show("flip [3,12) again", b)

	b.Insert(6, 1, 3)
	show("insert 111 @6", b)
	b.Insert(0, 0, 2)
	show("insert 00 @0", b)

	b.Delete(4, 15)
	show("delete [4,15)", b)

	fmt.Println()
	fmt.Printf("Rank(10, 1)   = %d\n", b.Rank(10, 1))
	fmt.Printf("Rank(10, 0)   = %d\n", b.Rank(10, 0))
	fmt.Printf("Select(3, 1)  = %d\n", b.Select(3, 1))
	fmt.Printf("Select(5, 0)  = %d\n", b.Select(5, 0))

	// A longer, more compressible sequence to show the byte win.
	big := rlebits.New("")
	for i := 0; i < 40; i++ {
		big.Insert(big.Len(), uint8(i%2), 500)
	}
	fmt.Println()
	show("40 runs x 500 bits", big)
	if err := big.CheckInvariants(); err != nil {
		panic(err)
	}
	fmt.Println("invariants OK")
}
