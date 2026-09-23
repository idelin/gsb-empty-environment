// pkvdemo runs the PKV1 test harness against the clean implementation and
// against a known-defective decoder, printing a sample message, the detected
// failure, its 1-minimal reduction, and a replay check.
package main

import (
	"encoding/hex"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"

	"pkv/buggy"
	"pkv/format"
	"pkv/gen"
	"pkv/harness"
)

const seed = 20260923

func main() {
	fmt.Println("PKV1 harness demo")
	fmt.Println("=================")

	r := rand.New(rand.NewSource(seed))
	sample := gen.ValidMessage(r)
	enc, _ := format.Encode(sample)
	fmt.Printf("sample message (%d records, %d bytes):\n%s\n\n", len(sample.Records), len(enc), hex.Dump(enc))

	clean := harness.SUT{Name: "format", Encode: format.Encode, Decode: format.Decode}
	cleanFailures := harness.Run(clean, seed, 300)
	fmt.Printf("clean SUT: %d failures over 300 iterations\n\n", len(cleanFailures))

	defective := harness.SUT{Name: "buggy(no-checksum)", Encode: format.Encode, Decode: buggy.Decode}
	buggyFailures := harness.Run(defective, seed, 300)
	fmt.Printf("buggy SUT: %d failures over 300 iterations\n", len(buggyFailures))
	if len(buggyFailures) == 0 {
		fmt.Println("ERROR: defective decoder was not detected")
		os.Exit(1)
	}
	f := buggyFailures[0]
	fmt.Printf("first failure: %s\n", f.Summary())
	minBytes, _ := hex.DecodeString(f.MinimalHex)
	fmt.Printf("minimal failing bytes (%d):\n%s\n", len(minBytes), hex.Dump(minBytes))
	fmt.Println("shrink log (1-minimal record):")
	for _, line := range f.ShrinkLog {
		fmt.Println("  " + line)
	}
	ok, why := f.Replay(defective)
	fmt.Printf("replay against buggy SUT: reproduced=%v (%s)\n", ok, why)
	okClean, whyClean := f.Replay(clean)
	fmt.Printf("replay against clean SUT: reproduced=%v (%s)\n", okClean, whyClean)

	path := filepath.Join(os.TempDir(), "pkv-failure.json")
	if err := f.Save(path); err != nil {
		fmt.Println("save:", err)
		os.Exit(1)
	}
	loaded, err := harness.LoadFailure(path)
	if err != nil {
		fmt.Println("load:", err)
		os.Exit(1)
	}
	okLoaded, _ := loaded.Replay(defective)
	fmt.Printf("saved to %s and reloaded; replay after reload: reproduced=%v\n", path, okLoaded)
}
