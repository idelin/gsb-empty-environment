// Command demo builds mph indexes over sample key sets and prints the
// seed/attempt statistics, the exact encoded layout footprint, and the
// result of known/unknown key lookups.
package main

import (
	"fmt"

	mph "example.com/perfectindex/mph"
)

func printFootprint(ix *mph.Index) {
	fp := ix.Footprint()
	fmt.Println("  encoded layout footprint (bytes):")
	fmt.Printf("    header        %6d   (magic, numKeys, numBuckets, attempts, seed, poolLen)\n", fp.Header)
	fmt.Printf("    displacements %6d   (uint16 x %d buckets)\n", fp.Displacements, ix.NumBuckets())
	fmt.Printf("    fingerprints  %6d   (uint8  x %d slots)\n", fp.Fingerprints, ix.Len())
	fmt.Printf("    key offsets   %6d   (uint32 x %d slots)\n", fp.KeyOffsets, ix.Len())
	fmt.Printf("    key pool      %6d   (original keys, slot order)\n", fp.KeyPool)
	fmt.Printf("    TOTAL         %6d   (%.2f bytes/key)\n", fp.Total, fp.BytesPerKey(ix.Len()))
}

func main() {
	fmt.Println("== small set: 12 language names ==")
	langs := []string{
		"go", "rust", "zig", "c", "cpp", "java",
		"kotlin", "swift", "python", "ruby", "lua", "haskell",
	}
	ix, err := mph.Build(langs)
	if err != nil {
		fmt.Println("build failed:", err)
		return
	}
	fmt.Printf("  built: n=%d seed=%d attempts=%d (bound %d)\n",
		ix.Len(), ix.Seed(), ix.Attempts(), mph.MaxSeedTries)
	for _, k := range langs {
		slot, ok := ix.Lookup(k)
		fmt.Printf("  lookup %-8q -> slot %2d, hit=%v\n", k, slot, ok)
	}
	for _, k := range []string{"cobol", "golang", "rusty", "Go"} {
		_, ok := ix.Lookup(k)
		fmt.Printf("  lookup %-8q -> hit=%v (unknown key rejected)\n", k, ok)
	}
	printFootprint(ix)

	fmt.Println()
	fmt.Println("== large set: 50000 synthetic keys ==")
	keys := make([]string, 50000)
	for i := range keys {
		keys[i] = fmt.Sprintf("user:%08d", i)
	}
	big, err := mph.Build(keys)
	if err != nil {
		fmt.Println("build failed:", err)
		return
	}
	fmt.Printf("  built: n=%d seed=%d attempts=%d (bound %d)\n",
		big.Len(), big.Seed(), big.Attempts(), mph.MaxSeedTries)
	printFootprint(big)
	misses := 0
	for _, k := range keys {
		if _, ok := big.Lookup(k); !ok {
			misses++
		}
	}
	falseHits := 0
	for i := 0; i < 200000; i++ {
		if _, ok := big.Lookup(fmt.Sprintf("user:%08d", 50000+i)); ok {
			falseHits++
		}
	}
	fmt.Printf("  verify: %d known-key misses, %d unknown-key false hits (200000 probes)\n",
		misses, falseHits)

	fmt.Println()
	fmt.Println("== reproducibility: same set, shuffled input ==")
	again, err := mph.Build(keys)
	if err != nil {
		fmt.Println("build failed:", err)
		return
	}
	fmt.Printf("  rebuild: seed=%d attempts=%d identical=%v\n",
		again.Seed(), again.Attempts(),
		again.Seed() == big.Seed() && again.Attempts() == big.Attempts())

	fmt.Println()
	fmt.Println("== duplicate input: diagnosed before build ==")
	_, err = mph.Build([]string{"a", "b", "a", "c", "c", "c"})
	fmt.Println("  build error:", err)
}
