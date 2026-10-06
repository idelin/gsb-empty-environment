package main

import (
	"fmt"
	"math/big"
	"math/rand"
	"os"
	"time"

	"convlib"
)

func report(name string, a, b []int64) bool {
	got, bound, mprod, err := convlib.Convolve(a, b)
	if err != nil {
		fmt.Printf("[%s] ERROR: %v (bound B=%s, product M=%s)\n", name, err, bound, mprod)
		return false
	}
	want := convlib.ConvolveNaive(a, b)
	for i := range got {
		if got[i].Cmp(want[i]) != 0 {
			fmt.Printf("[%s] MISMATCH at %d: got %s want %s\n", name, i, got[i], want[i])
			return false
		}
	}
	fmt.Printf("[%s] OK  len=%d  coefficient bound B=%s  modulus product M=%s  headroom M/(2B)=%s\n",
		name, len(got), bound, mprod,
		new(big.Rat).SetFrac(mprod, new(big.Int).Lsh(bound, 1)).FloatString(3))
	return true
}

func main() {
	rng := rand.New(rand.NewSource(time.Now().UnixNano()))
	ok := true

	// Random short sequences with negative entries.
	for trial := 0; trial < 5; trial++ {
		a := make([]int64, 1+rng.Intn(20))
		b := make([]int64, 1+rng.Intn(20))
		for i := range a {
			a[i] = rng.Int63n(2e9) - 1e9
		}
		for i := range b {
			b[i] = rng.Int63n(2e9) - 1e9
		}
		ok = report(fmt.Sprintf("random-%d", trial), a, b) && ok
	}

	// Near-boundary: middle coefficient saturates B at >99% of M/2.
	const m = 1000
	a := make([]int64, m)
	b := make([]int64, m)
	for i := range a {
		a[i] = 1000000
		b[i] = -500000000
	}
	ok = report("near-boundary-negative", a, b) && ok

	if !ok {
		os.Exit(1)
	}
	fmt.Println("all checks passed")
}
