// Command doddemo demonstrates the dod archive: writing constant-step,
// irregular and reset-anchored series, time-range seek, local correction
// with old index versions retained, and bounded decode.
package main

import (
	"fmt"
	"math/rand"

	"dod"
)

func main() {
	st := &dod.MemStore{}
	a := dod.NewArchive(st, dod.AllowRegression)

	// 1) constant-step series: second-order deltas are all zero.
	for i := 0; i < 1000; i++ {
		must(a.Add(int64(i*10), int64(5*i+3)))
	}
	// 2) irregular intervals with negative deltas.
	rng := rand.New(rand.NewSource(1))
	t, v := int64(10000), int64(0)
	for i := 0; i < 1000; i++ {
		t += 1 + rng.Int63n(100)
		v += rng.Int63n(2001) - 1000
		must(a.Add(t, v))
	}
	// 3) explicit reset: time restarts under a new epoch.
	must(a.Reset())
	for i := 0; i < 500; i++ {
		must(a.Add(int64(i*20), int64(i*i)))
	}
	must(a.Flush())

	fmt.Printf("archive: %d bytes for 2500 points (raw int64 would be %d bytes)\n",
		st.Len(), 2500*16)
	fmt.Println("blocks:")
	for i, e := range a.Index().Entries {
		fmt.Printf("  #%d epoch=%d t=[%d..%d] n=%d len=%dB v%d\n",
			i, e.Epoch, e.T0, e.T1, e.Count, e.Length, e.Version)
	}

	// 4) time-range seek via the block index.
	pos := a.SeekTime(20000)
	ts, vs, err := a.ReadRange(20000, 20100, 1000)
	must(err)
	fmt.Printf("seek t=20000 -> block #%d; range [20000,20100] -> %d points, first=(%d,%d)\n",
		pos, len(ts), ts[0], vs[0])

	// 5) local correction: re-encode one block, old index version retained.
	cpos := a.SeekTime(1500)
	blk, err := a.ReadBlock(cpos)
	must(err)
	fixed := append([]int64{}, blk.Values...)
	fixed[0] = -12345
	must(a.Correct(cpos, blk.Times, fixed))
	fmt.Printf("after correcting live block #%d (epoch=%d t=[%d..%d]):\n",
		cpos, blk.Epoch, blk.Times[0], blk.Times[len(blk.Times)-1])
	for i, e := range a.Index().Entries {
		state := "live"
		if e.Superseded {
			state = "superseded"
		}
		fmt.Printf("  #%d epoch=%d t=[%d..%d] n=%d v%d %s\n", i, e.Epoch, e.T0, e.T1, e.Count, e.Version, state)
	}
	nb, err := a.ReadBlock(cpos)
	must(err)
	fmt.Printf("corrected block #1 first value = %d\n", nb.Values[0])

	// 6) bounded decode.
	a.SetMaxValues(10)
	if _, err := a.ReadBlock(0); err != nil {
		fmt.Printf("bounded decode enforced: %v\n", err)
	}

	// 7) index round trip.
	ix, err := dod.DecodeIndex(a.Index().Encode())
	must(err)
	fmt.Printf("index serialized: %d entries round-trip ok=%v\n",
		len(ix.Entries), len(ix.Entries) == len(a.Index().Entries))

	// 8) regression policy.
	strict := dod.NewArchive(&dod.MemStore{}, dod.RejectRegression)
	must(strict.Add(100, 1))
	if err := strict.Add(50, 2); err != nil {
		fmt.Printf("reject policy: %v\n", err)
	}
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}
