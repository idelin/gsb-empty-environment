// Command efdemo builds an Elias-Fano index over a deterministic random
// sequence, prints the real byte ledger, exercises random access,
// lower_bound and duplicate ranks, then merges two indexes.
package main

import (
	"fmt"
	"math/rand/v2"
	"slices"

	"efano"
)

func main() {
	const n = 1_000_000
	const universe = uint64(1) << 32

	rng := rand.New(rand.NewPCG(42, 7))
	vals := make([]uint64, n)
	for i := range vals {
		vals[i] = rng.Uint64() % universe
	}
	slices.Sort(vals)

	l := efano.ChooseL(universe, n)
	fmt.Printf("n=%d universe=2^32 -> l=%d (floor(log2(u/n)))\n", n, l)

	// Bounded-memory streaming build: values flow in one at a time.
	b := efano.NewBuilder(l)
	for _, v := range vals {
		if err := b.Add(v); err != nil {
			panic(err)
		}
	}
	ef := b.Build()

	st := ef.Stats()
	raw := uint64(n) * 8
	fmt.Println("--- byte ledger ---")
	fmt.Printf("high bits (unary gaps) : %12d bits\n", st.HighBits)
	fmt.Printf("low bits  (packed)     : %12d bits\n", st.LowBits)
	fmt.Printf("rank directory         : %12d bytes\n", st.RankBytes)
	fmt.Printf("index in memory        : %12d bytes\n", st.IndexBytes)
	fmt.Printf("serialized (with hdr)  : %12d bytes\n", st.SerializedBytes)
	fmt.Printf("plain []uint64         : %12d bytes\n", raw)
	fmt.Printf("bits/value             : %12.2f (log2(u/n) = %.2f)\n",
		st.BitsPerValue, 32.0-20.0) // log2(2^32 / 10^6)
	fmt.Printf("compression vs raw     : %12.1fx\n", float64(raw)/float64(st.IndexBytes))

	fmt.Println("--- queries ---")
	fmt.Printf("At(0)=%d At(n/2)=%d At(n-1)=%d\n", ef.At(0), ef.At(n/2), ef.At(n-1))
	q := vals[n/3]
	s, e := ef.EqualRange(q)
	fmt.Printf("LowerBound(%d)=%d EqualRange=[%d,%d) Count=%d\n", q, ef.LowerBound(q), s, e, ef.Count(q))
	fmt.Printf("LowerBound(2^40)=%d (past the end, no huge allocation)\n", ef.LowerBound(uint64(1)<<40))

	fmt.Println("--- merge of two encoded halves (no full decode) ---")
	half := n / 2
	a, _ := efano.Encode(vals[:half], l)
	c, _ := efano.Encode(vals[half:], l)
	m, err := efano.Merge(a, c)
	if err != nil {
		panic(err)
	}
	ok := m.Len() == n && m.At(n-1) == vals[n-1] && m.Verify() == nil
	fmt.Printf("merged n=%d, max matches=%v, verify ok=%v\n", m.Len(), m.At(n-1) == vals[n-1], ok)
	_ = ok

	fmt.Println("--- verifiable format ---")
	data := ef.MarshalBinary()
	back, err := efano.Parse(data)
	if err != nil {
		panic(err)
	}
	fmt.Printf("serialized %d bytes, Parse ok, Verify ok=%v\n", len(data), back.Verify() == nil)
}
