// Command adaptive-huffman compresses or decompresses a stream using the
// FGK adaptive Huffman algorithm. Data flows through stdin/stdout so the
// tool can be chained in pipelines.
//
//	adaptive-huffman        < plain.txt  > plain.ah   # compress
//	adaptive-huffman -d     < plain.ah   > plain.txt  # decompress
package main

import (
	"flag"
	"fmt"
	"io"
	"os"

	huffman "adaptivehuffman"
)

func main() {
	decode := flag.Bool("d", false, "decompress (default: compress)")
	flag.Parse()

	if *decode {
		if _, err := io.Copy(os.Stdout, huffman.NewDecoder(os.Stdin)); err != nil {
			fmt.Fprintln(os.Stderr, "adaptive-huffman: decode:", err)
			os.Exit(1)
		}
		return
	}
	enc := huffman.NewEncoder(os.Stdout)
	if _, err := io.Copy(enc, os.Stdin); err != nil {
		fmt.Fprintln(os.Stderr, "adaptive-huffman: encode:", err)
		os.Exit(1)
	}
	if err := enc.Close(); err != nil {
		fmt.Fprintln(os.Stderr, "adaptive-huffman: flush:", err)
		os.Exit(1)
	}
}
