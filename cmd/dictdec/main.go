// Command dictdec is the independent decoder for DZC1 files. It depends
// only on the wire format package, never on compressor code.
//
//	dictdec -in <file.dzc> -out <file> [-maxout N]
package main

import (
	"flag"
	"fmt"
	"os"

	"dictcomp/internal/wire"
)

func main() {
	in := flag.String("in", "", "input .dzc file")
	out := flag.String("out", "", "output file")
	maxOut := flag.Int64("maxout", 1<<30, "maximum decompressed size in bytes")
	flag.Parse()
	if *in == "" || *out == "" {
		fmt.Fprintln(os.Stderr, "usage: dictdec -in <file.dzc> -out <file> [-maxout N]")
		os.Exit(2)
	}
	data, err := os.ReadFile(*in)
	if err != nil {
		fmt.Fprintln(os.Stderr, "dictdec:", err)
		os.Exit(1)
	}
	_, plain, err := wire.Decode(data, *maxOut)
	if err != nil {
		fmt.Fprintln(os.Stderr, "dictdec:", err)
		os.Exit(1)
	}
	if err := os.WriteFile(*out, plain, 0o644); err != nil {
		fmt.Fprintln(os.Stderr, "dictdec:", err)
		os.Exit(1)
	}
	fmt.Printf("dictdec: %d -> %d bytes (length and CRC-32 verified)\n", len(data), len(plain))
}
