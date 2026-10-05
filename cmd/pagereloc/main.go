// Command pagereloc compacts a page file offline: it renumbers pages
// densely, rewrites all in-file references, preserves external stable
// object IDs, and publishes the new file plus a relocation table only
// after full validation.
package main

import (
	"flag"
	"fmt"
	"os"

	"pagereloc/pagefile"
)

func main() {
	in := flag.String("in", "", "input page file")
	out := flag.String("out", "", "output compacted page file")
	table := flag.String("table", "", "output relocation table (JSON)")
	flag.Parse()
	if *in == "" || *out == "" || *table == "" {
		fmt.Fprintln(os.Stderr, "usage: pagereloc -in old.bin -out new.bin -table reloc.json")
		flag.PrintDefaults()
		os.Exit(2)
	}
	tbl, err := pagefile.CompactFile(*in, *out, *table, nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "pagereloc: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("compacted %s -> %s\n", *in, *out)
	fmt.Printf("  pages relocated: %d\n", len(tbl.Entries))
	fmt.Printf("  root: %d -> %d\n", tbl.RootOld, tbl.RootNew)
	fmt.Printf("  external IDs preserved: %d\n", len(tbl.External))
	fmt.Printf("  relocation table: %s\n", *table)
}
