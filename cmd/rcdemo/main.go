// Command rcdemo demonstrates checkpointed range coding: it encodes input
// with periodic checkpoints, then verifies full decoding, random-access
// per-segment decoding, and bit-identical resumed encoding.
//
// Usage:
//
//	go run ./cmd/rcdemo [-in file] [-seg 4096] [-out prefix]
package main

import (
	"bytes"
	"flag"
	"fmt"
	"log"
	"os"

	rc "rangecoder"
)

func main() {
	in := flag.String("in", "", "input file (default: built-in sample text)")
	seg := flag.Int("seg", 4096, "segment size in bytes (checkpoint interval)")
	out := flag.String("out", "", "output prefix: write PREFIX.rc and PREFIX.ckt")
	flag.Parse()

	data, err := readInput(*in)
	if err != nil {
		log.Fatal(err)
	}

	// Encode with a checkpoint after every segment.
	enc := rc.NewEncoder()
	for off := 0; off < len(data); off += *seg {
		end := off + *seg
		if end > len(data) {
			end = len(data)
		}
		enc.Encode(data[off:end])
		enc.Checkpoint()
	}
	stream := enc.Finish()
	cps := enc.Checkpoints()
	ratio := 0.0
	if len(data) > 0 {
		ratio = 100 * float64(len(stream)) / float64(len(data))
	}
	fmt.Printf("encoded: %d bytes -> %d bytes (%.1f%%), %d checkpoints\n",
		len(data), len(stream), ratio, len(cps))

	// 1. Full round trip (segment-by-segment via the checkpoint table).
	full, err := rc.DecodeSegments(stream, cps, uint64(len(data))+1)
	check("full decode", err)
	check("full decode matches", equalCheck(full, data))
	fmt.Println("full decode: OK")

	// 2. Random-access decode of every segment from its checkpoint.
	prev := rc.Checkpoint{Offset: rc.StreamHeaderLen, Model: rc.NewModel()}
	for i, cp := range cps {
		n := cp.SymbolIndex - prev.SymbolIndex
		d, err := rc.NewDecoder(stream, prev)
		check(fmt.Sprintf("segment %d decoder", i), err)
		got, err := d.Decode(n)
		check(fmt.Sprintf("segment %d decode", i), err)
		check(fmt.Sprintf("segment %d matches", i),
			equalCheck(got, data[prev.SymbolIndex:cp.SymbolIndex]))
		prev = cp
	}
	// Tail after the last checkpoint (up to EOF).
	d, err := rc.NewDecoder(stream, prev)
	check("tail decoder", err)
	tail, err := d.Decode(uint64(len(data)) - prev.SymbolIndex)
	check("tail decode", err)
	check("tail matches", equalCheck(tail, data[prev.SymbolIndex:]))
	fmt.Printf("random-access decode: OK (%d segments + tail)\n", len(cps))

	// 3. Resume from the middle checkpoint: bitstream must be identical.
	mid := cps[len(cps)/2]
	re, err := rc.ResumeEncoder(stream, mid)
	check("resume", err)
	for off := int(mid.SymbolIndex); off < len(data); off += *seg {
		end := off + *seg
		if end > len(data) {
			end = len(data)
		}
		re.Encode(data[off:end])
		re.Checkpoint()
	}
	resumed := re.Finish()
	check("resumed stream identical", equalCheck(resumed, stream))
	fmt.Printf("resume from checkpoint %d (offset %d, symbol %d): bitstream identical\n",
		len(cps)/2, mid.Offset, mid.SymbolIndex)

	// 4. Checkpoint table round trip through its serialized form.
	blob := rc.MarshalCheckpointTable(cps)
	parsed, err := rc.ParseCheckpointTable(blob)
	check("checkpoint table parse", err)
	check("checkpoint table matches",
		equalCheck(parsed[0].Model.Marshal(), cps[0].Model.Marshal()))
	fmt.Printf("checkpoint table: %d bytes, verified (magic+version+length+CRC32)\n", len(blob))

	if *out != "" {
		check("write stream", os.WriteFile(*out+".rc", stream, 0o644))
		check("write table", os.WriteFile(*out+".ckt", blob, 0o644))
		fmt.Printf("wrote %s.rc and %s.ckt\n", *out, *out)
	}
}

func readInput(path string) ([]byte, error) {
	if path != "" {
		return os.ReadFile(path)
	}
	var b bytes.Buffer
	for b.Len() < 60000 {
		b.WriteString("The quick brown fox jumps over the lazy dog. ")
		b.WriteString("Range coding with checkpoints enables random access. ")
	}
	return b.Bytes(), nil
}

func equalCheck(a, b []byte) error {
	if !bytes.Equal(a, b) {
		return fmt.Errorf("mismatch: %d vs %d bytes", len(a), len(b))
	}
	return nil
}

func check(what string, err error) {
	if err != nil {
		log.Fatalf("%s: %v", what, err)
	}
}
