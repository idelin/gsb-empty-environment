// Command bwt encodes and decodes BWT block files and locates
// fragments inside an encoded block.
//
//	bwt encode <input> <output.bwtk>
//	bwt decode <input.bwtk> <output>
//	bwt locate <input.bwtk> <fragment>
package main

import (
	"bufio"
	"fmt"
	"os"

	"bwtblock/bwt"
)

// decodeBudget caps a single decoded block at 2 GiB.
const decodeBudget = bwt.MaxBlockSize

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "bwt:", err)
	os.Exit(1)
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage:")
	fmt.Fprintln(os.Stderr, "  bwt encode <input> <output.bwtk>")
	fmt.Fprintln(os.Stderr, "  bwt decode <input.bwtk> <output>")
	fmt.Fprintln(os.Stderr, "  bwt locate <input.bwtk> <fragment>")
	os.Exit(2)
}

func main() {
	if len(os.Args) < 2 {
		usage()
	}
	switch os.Args[1] {
	case "encode":
		if len(os.Args) != 4 {
			usage()
		}
		encode(os.Args[2], os.Args[3])
	case "decode":
		if len(os.Args) != 4 {
			usage()
		}
		decode(os.Args[2], os.Args[3])
	case "locate":
		if len(os.Args) != 4 {
			usage()
		}
		locate(os.Args[2], os.Args[3])
	default:
		usage()
	}
}

func encode(inPath, outPath string) {
	data, err := os.ReadFile(inPath)
	if err != nil {
		fatal(err)
	}
	f, err := os.Create(outPath)
	if err != nil {
		fatal(err)
	}
	w := bufio.NewWriter(f)
	if err := bwt.Encode(w, data); err != nil {
		fatal(err)
	}
	if err := w.Flush(); err != nil {
		fatal(err)
	}
	if err := f.Close(); err != nil {
		fatal(err)
	}
	fmt.Printf("encoded %d bytes -> %s\n", len(data), outPath)
}

func decode(inPath, outPath string) {
	f, err := os.Open(inPath)
	if err != nil {
		fatal(err)
	}
	defer f.Close()
	blk, err := bwt.Decode(bufio.NewReader(f), decodeBudget)
	if err != nil {
		fatal(err)
	}
	out, err := blk.Inverse()
	if err != nil {
		fatal(err)
	}
	if err := os.WriteFile(outPath, out, 0o644); err != nil {
		fatal(err)
	}
	fmt.Printf("decoded %d bytes -> %s\n", len(out), outPath)
}

func locate(inPath, fragment string) {
	f, err := os.Open(inPath)
	if err != nil {
		fatal(err)
	}
	defer f.Close()
	blk, err := bwt.Decode(bufio.NewReader(f), decodeBudget)
	if err != nil {
		fatal(err)
	}
	idx, err := blk.Index()
	if err != nil {
		fatal(err)
	}
	positions := idx.Locate([]byte(fragment))
	if len(positions) == 0 {
		fmt.Println("no occurrences")
		return
	}
	w := bufio.NewWriter(os.Stdout)
	defer w.Flush()
	fmt.Fprintf(w, "%d occurrence(s):", len(positions))
	for _, p := range positions {
		fmt.Fprintf(w, " %d", p)
	}
	fmt.Fprintln(w)
}
