// Command bwt encodes and decodes files with the block BWT container format.
//
//	bwt encode <in> <out>
//	bwt decode <in> <out> [maxLen]
//
// maxLen bounds the decoded output size (bytes); default 1 GiB.
package main

import (
	"fmt"
	"os"
	"strconv"

	"bwt"
)

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "bwt:", err)
	os.Exit(1)
}

func main() {
	if len(os.Args) < 4 {
		fmt.Fprintln(os.Stderr, "usage: bwt encode <in> <out> | bwt decode <in> <out> [maxLen]")
		os.Exit(2)
	}
	mode, inPath, outPath := os.Args[1], os.Args[2], os.Args[3]
	in, err := os.Open(inPath)
	if err != nil {
		fatal(err)
	}
	defer in.Close()

	switch mode {
	case "encode":
		data, err := os.ReadFile(inPath)
		if err != nil {
			fatal(err)
		}
		out, err := os.Create(outPath)
		if err != nil {
			fatal(err)
		}
		if err := bwt.Write(out, data); err != nil {
			fatal(err)
		}
		if err := out.Close(); err != nil {
			fatal(err)
		}
		fmt.Printf("encoded %d bytes -> %s\n", len(data), outPath)
	case "decode":
		maxLen := uint64(1) << 30
		if len(os.Args) > 4 {
			v, err := strconv.ParseUint(os.Args[4], 10, 64)
			if err != nil {
				fatal(err)
			}
			maxLen = v
		}
		data, err := bwt.Read(in, maxLen)
		if err != nil {
			fatal(err)
		}
		if err := os.WriteFile(outPath, data, 0o644); err != nil {
			fatal(err)
		}
		fmt.Printf("decoded %d bytes -> %s\n", len(data), outPath)
	default:
		fmt.Fprintln(os.Stderr, "unknown mode:", mode)
		os.Exit(2)
	}
}
