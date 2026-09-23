// Command demo runs the KVS1 harness end to end: it prints a generated
// sample, the single-point mutation table, corpus checks against the
// reference decoder, and defect detection plus shrinking against the
// known-buggy decoder.
package main

import (
	"encoding/hex"
	"flag"
	"fmt"
	"math/rand"

	"kvsfmt/buggy"
	"kvsfmt/format"
	"kvsfmt/fuzz"
	"kvsfmt/oracle"
)

func main() {
	seed := flag.Int64("seed", 1, "generator seed")
	n := flag.Int("n", 200, "corpus size")
	flag.Parse()

	fmt.Printf("== KVS1 harness demo (seed=%d, corpus=%d) ==\n\n", *seed, *n)

	rng := rand.New(rand.NewSource(*seed))
	entries, data := fuzz.Generate(rng)
	fmt.Println("-- generated valid sample --")
	for _, e := range entries {
		fmt.Printf("  entry key=%q value=%x\n", e.Key, e.Value)
	}
	fmt.Printf("  bytes (%d): %s\n\n", len(data), hex.EncodeToString(data))

	fmt.Println("-- single-point mutations vs reference decoder --")
	for _, m := range fuzz.Mutations() {
		mb := m.Apply(data)
		if mb == nil {
			fmt.Printf("  %-16s skipped (not applicable)\n", m.Name)
			continue
		}
		_, err := format.Decode(mb)
		fmt.Printf("  %-16s breaks=%-10s oracle=%-10s decoder=%v\n",
			m.Name, m.Class, oracle.Classify(mb), err)
	}
	fmt.Println()

	fmt.Println("-- corpus check: reference decoder --")
	fails := fuzz.RunCorpus(*seed, *n, format.Decode)
	fmt.Printf("  failures: %d\n\n", len(fails))

	fmt.Println("-- corpus check: buggy decoder (known defect: no CRC check) --")
	fails = fuzz.RunCorpus(*seed, *n, buggy.Decode)
	fmt.Printf("  failures: %d\n", len(fails))
	if len(fails) == 0 {
		fmt.Println("  BUG NOT DETECTED")
		return
	}
	f := fails[0]
	fmt.Printf("  first failure: kind=%s mut=%s want=%s got=%s\n", f.Kind, f.Mut, f.Want, f.Got)
	fmt.Printf("  replay: seed=%d bytes=%s\n\n", f.Seed, hex.EncodeToString(f.Data))

	fmt.Println("-- shrinking first failure (same error class preserved) --")
	pred := func(b []byte) bool {
		if oracle.Classify(b) != string(format.ClassCRC) {
			return false
		}
		_, err := buggy.Decode(b)
		return err == nil
	}
	res := fuzz.Shrink(f.Data, pred)
	fmt.Printf("  checks performed: %d\n", res.Checks)
	fmt.Printf("  minimal failing bytes (%d): %s\n", len(res.Minimal), hex.EncodeToString(res.Minimal))
	fmt.Printf("  replay minimal: reproduces=%v\n", pred(res.Minimal))
	fmt.Println("  shrink log (tail):")
	log := res.Log
	if len(log) > 12 {
		log = log[len(log)-12:]
	}
	for _, line := range log {
		fmt.Println("   ", line)
	}
}
