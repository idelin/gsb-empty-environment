// Command gen-golden regenerates the deterministic golden fixtures under
// testdata. Keys come from fixed seeds, signatures are real Ed25519.
package main

import (
	"fmt"
	"os"

	"prescreen.local/ddd-10561-v4/testsupport"
)

func main() {
	dir := "."
	if len(os.Args) > 1 {
		dir = os.Args[1]
	}
	for _, fixture := range testsupport.AllFixtures() {
		if err := testsupport.WriteGolden(dir, fixture); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		fmt.Println("wrote", testsupport.GoldenPath(dir, fixture.Name))
	}
}
