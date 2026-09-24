// Command secded demonstrates the extended Hamming SECDED package:
// encoding a file into the interleaved container format, decoding it,
// and a self-contained demo of single-error correction, double-error
// detection, burst handling and erasure recovery.
//
// Usage:
//
//	secded encode <in> <out>            encode a file
//	secded decode <in> <out>            decode a container
//	secded demo                         run the corruption demo
package main

import (
	"fmt"
	"os"

	"secded"
)

const (
	demoM     = 6 // Hamming(64,57) extended
	demoDepth = secded.DefaultDepth
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "encode":
		err = runEncode(os.Args[2], os.Args[3])
	case "decode":
		err = runDecode(os.Args[2], os.Args[3])
	case "demo":
		err = runDemo()
	default:
		usage()
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: secded encode <in> <out> | decode <in> <out> | demo")
}

func runEncode(in, out string) error {
	src, err := os.ReadFile(in)
	if err != nil {
		return err
	}
	blob, err := secded.Encode(src, demoM, demoDepth)
	if err != nil {
		return err
	}
	fmt.Printf("encoded %d bytes -> %d bytes (m=%d depth=%d)\n", len(src), len(blob), demoM, demoDepth)
	return os.WriteFile(out, blob, 0o644)
}

func runDecode(in, out string) error {
	blob, err := os.ReadFile(in)
	if err != nil {
		return err
	}
	res, err := secded.Decode(blob, nil)
	if res != nil {
		report(res)
	}
	if res == nil {
		return err
	}
	if werr := os.WriteFile(out, res.Data, 0o644); werr != nil {
		return werr
	}
	return err
}

func report(res *secded.Result) {
	fmt.Printf("decoded %d bytes, %d blocks\n", len(res.Data), len(res.Blocks))
	fmt.Printf("  corrected blocks:     %v\n", res.Corrected)
	fmt.Printf("  uncorrectable blocks: %v\n", res.Uncorrectable)
}

func runDemo() error {
	msg := []byte("SECDED demo: extended Hamming protects this sentence against bit rot.\n")
	blob, err := secded.Encode(msg, demoM, demoDepth)
	if err != nil {
		return err
	}
	fmt.Printf("message %d bytes -> container %d bytes\n\n", len(msg), len(blob))

	// 1. clean round trip
	show("clean", blob, nil)

	const hdr = 36 * 8 // container header size in bits

	// 2. single bit flip in the payload -> corrected
	c1 := flipBits(blob, hdr+100)
	show("single payload bit flip", c1, nil)

	// 3. double bit flip in one codeword -> detected, never "repaired"
	c2 := flipBits(blob, hdr+100, hdr+100+demoDepth)
	show("double bit flip in one codeword", c2, nil)

	// 4. burst of depth bits, unmarked -> corrected by interleaving
	c3 := flipRange(blob, hdr+200, demoDepth)
	show(fmt.Sprintf("burst of %d bits (unmarked)", demoDepth), c3, nil)

	// 5. burst of 3*depth bits, marked as erasure -> recovered
	c4 := flipRange(blob, hdr+400, 3*demoDepth)
	show(fmt.Sprintf("burst of %d bits (marked as erasure)", 3*demoDepth), c4,
		&secded.Options{Erasures: []secded.BitRange{{Start: 400, Len: 3 * demoDepth}}})

	// 6. burst beyond capability, marked -> honestly reported
	c5 := flipRange(blob, hdr+600, 3*demoDepth+1)
	show(fmt.Sprintf("burst of %d bits (marked, beyond capability)", 3*demoDepth+1), c5,
		&secded.Options{Erasures: []secded.BitRange{{Start: 600, Len: 3*demoDepth + 1}}})
	return nil
}

func show(label string, blob []byte, opts *secded.Options) {
	res, err := secded.Decode(blob, opts)
	fmt.Printf("%-52s -> ", label)
	if res == nil {
		fmt.Println("rejected:", err)
		return
	}
	switch {
	case len(res.Uncorrectable) > 0:
		fmt.Printf("UNCORRECTABLE blocks %v (reported, not guessed)\n", res.Uncorrectable)
	case len(res.Corrected) > 0:
		fmt.Printf("corrected blocks %v, data intact: %v\n", res.Corrected, err == nil)
	default:
		fmt.Println("clean")
	}
}

func flipBits(blob []byte, bits ...int) []byte {
	out := append([]byte(nil), blob...)
	for _, b := range bits {
		out[b/8] ^= 1 << uint(b%8)
	}
	return out
}

func flipRange(blob []byte, start, n int) []byte {
	out := append([]byte(nil), blob...)
	for t := start; t < start+n && t/8 < len(out); t++ {
		out[t/8] ^= 1 << uint(t%8)
	}
	return out
}
