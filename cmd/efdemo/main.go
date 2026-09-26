// Command efdemo demonstrates the Elias-Fano index: real bit layout,
// byte accounting, lower_bound semantics, streaming build and merge.
package main

import (
	"eliasfano"
	"fmt"
)

func main() {
	fmt.Println("=== 1. Encoding layout: values=[1 2 2 3 5 8 13 21 34 55 89 144], U=256 ===")
	vals := []uint64{1, 2, 2, 3, 5, 8, 13, 21, 34, 55, 89, 144}
	ef, err := eliasfano.New(vals, 256)
	must(err)
	printLayout(ef)
	fmt.Printf("high unary bitmap : %s\n", ef.HighString())
	fmt.Printf("  ('1' = value marker, '0' = bucket separator; i-th 1 sits at high_i+i)\n")
	fmt.Printf("low bits (l=%d)   :", ef.LowBits())
	for i := uint64(0); i < ef.N(); i++ {
		fmt.Printf(" %02d", vals[i]&((1<<ef.LowBits())-1))
	}
	fmt.Println()

	fmt.Println("\n=== 2. Random access & lower_bound (exact ranks, duplicates) ===")
	for _, q := range []uint64{0, 2, 4, 144, 145, 255, 256, 1 << 40} {
		fmt.Printf("LowerBound(%-12d) = %d   Count(%d) = %d\n", q, ef.LowerBound(q), q, ef.Count(q))
	}
	fmt.Print("Access(i): ")
	for i := uint64(0); i < ef.N(); i++ {
		fmt.Printf("%d ", ef.Access(i))
	}
	fmt.Println()

	fmt.Println("\n=== 3. Edge cases: empty set, all-same values, huge universe ===")
	empty := eliasfano.NewBuilder(0, 0).MustBuild()
	fmt.Printf("empty: n=%d bytes=%d LowerBound(2^63)=%d\n",
		empty.N(), empty.SizeBytes(), empty.LowerBound(1<<63))

	same := eliasfano.NewBuilder(1000, 6)
	for i := 0; i < 1000; i++ {
		must(same.Add(5))
	}
	sef := same.MustBuild()
	fmt.Printf("1000x value 5, U=6: l=%d (low array empty) bytes=%d Count(5)=%d LowerBound(6)=%d\n",
		sef.LowBits(), sef.SizeBytes(), sef.Count(5), sef.LowerBound(6))

	huge := eliasfano.NewBuilder(4, 1<<62)
	for _, v := range []uint64{0, 1 << 40, 1 << 61, (1 << 62) - 1} {
		must(huge.Add(v))
	}
	hef := huge.MustBuild()
	fmt.Printf("n=4, U=2^62: l=%d bytes=%d (no universe-proportional allocation)\n",
		hef.LowBits(), hef.SizeBytes())

	fmt.Println("\n=== 4. Merge of two encoded sequences (direct high/low walk) ===")
	a, err := eliasfano.New([]uint64{1, 4, 4, 9, 16, 25}, 64)
	must(err)
	b, err := eliasfano.New([]uint64{2, 4, 8, 32, 64}, 128)
	must(err)
	m := eliasfano.Merge(a, b)
	fmt.Printf("A (n=%d,U=%d,l=%d) + B (n=%d,U=%d,l=%d) -> M (n=%d,U=%d,l=%d)\n",
		a.N(), a.Universe(), a.LowBits(), b.N(), b.Universe(), b.LowBits(),
		m.N(), m.Universe(), m.LowBits())
	fmt.Print("merged: ")
	it := m.Iter()
	for v, ok := it.Next(); ok; v, ok = it.Next() {
		fmt.Printf("%d ", v)
	}
	fmt.Println()

	fmt.Println("\n=== 5. Byte accounting vs parameter derivation ===")
	fmt.Println("total bits <= n*l (low) + n + numBuckets (high), numBuckets <= 2n")
	fmt.Println("=> bits/value in [l+2, l+3],  l = max(0, floor(log2(U/n)))")
	fmt.Printf("%-14s %-8s %-4s %-12s %-14s %-14s\n", "n", "U", "l", "bytes", "bits/value", "bound (l+3)")
	for _, tc := range [][2]uint64{
		{1000, 1 << 10}, {1000, 1 << 16}, {1000, 1 << 24},
		{100000, 1 << 32}, {1000000, 1 << 40},
	} {
		n, u := tc[0], tc[1]
		bld := eliasfano.NewBuilder(n, u)
		// Non-decreasing synthetic stream: i * U / n (duplicates included).
		for i := uint64(0); i < n; i++ {
			must(bld.Add(i * (u / n)))
		}
		e := bld.MustBuild()
		lay := e.Layout()
		fmt.Printf("%-14d %-8d %-4d %-12d %-14.2f %-14d\n",
			n, u, lay.LowBits, lay.TotalBytes, lay.BitsPerValue, lay.LowBits+3)
	}
	fmt.Println("(bits/value counts payload only; TotalBytes adds 8B magic, uvarint header, 8B CRC64)")
}

func printLayout(ef *eliasfano.EliasFano) {
	lay := ef.Layout()
	fmt.Printf("n=%d U=%d l=%d numBuckets=%d highBits=%d lowWords=%d highWords=%d\n",
		lay.N, lay.Universe, lay.LowBits, lay.NumBuckets, lay.HighBitsLen,
		lay.LowWords, lay.HighWords)
	fmt.Printf("payload=%dB total=%dB (%.2f bits/value payload)\n",
		lay.PayloadBytes, lay.TotalBytes, lay.BitsPerValue)
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}
