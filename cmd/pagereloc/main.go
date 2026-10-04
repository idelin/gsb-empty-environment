// Command pagereloc compacts a page file offline.
//
// Usage: pagereloc <src> <dst>
//
// Writes <dst> and <dst>.reloc only if all validation passes; <src> is
// never modified.
package main

import (
	"fmt"
	"os"

	"pagereloc"
)

func main() {
	if len(os.Args) != 3 {
		fmt.Fprintf(os.Stderr, "usage: %s <src> <dst>\n", os.Args[0])
		os.Exit(2)
	}
	table, err := pagereloc.Compact(os.Args[1], os.Args[2], nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "pagereloc: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("compacted %d pages; relocation table at %s.reloc\n",
		len(table.OldToNew), os.Args[2])
}
