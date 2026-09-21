// Command example demonstrates the streamcol format end to end: encoding a
// timestamp-like series, sequential and random-access decoding, byte-by-byte
// streaming, checkpoint/resume, and recovery after block corruption.
package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"math/rand"
	"os"

	"streamcol"
)

func check(ok bool, msg string, args ...any) {
	if !ok {
		fmt.Printf("FAIL: "+msg+"\n", args...)
		os.Exit(1)
	}
}

func main() {
	ctx := context.Background()

	// A nanosecond timestamp series with ~1s steps and small jitter:
	// friendly to delta-of-delta encoding.
	r := rand.New(rand.NewSource(42))
	vals := make([]int64, 5000)
	cur := int64(1_700_000_000_000_000_000)
	for i := range vals {
		cur += 1_000_000_000 + int64(r.Intn(2001)-1000)
		vals[i] = cur
	}

	var buf bytes.Buffer
	enc := streamcol.NewEncoder(&buf, &streamcol.EncoderConfig{
		BlockSize:        256,
		Encoding:         streamcol.EncAuto,
		IndependentEvery: 4,
	})
	for _, v := range vals {
		if err := enc.Add(v); err != nil {
			panic(err)
		}
	}
	if err := enc.Close(); err != nil {
		panic(err)
	}
	data := buf.Bytes()
	idx := enc.Index()
	fmt.Printf("encoded %d values -> %d bytes (%.2f B/value), %d blocks\n",
		len(vals), len(data), float64(len(data))/float64(len(vals)), len(idx))
	for i, e := range idx {
		fmt.Printf("  block %2d @%-6d count=%-4d enc=%-14s independent=%v\n",
			i, e.Offset, e.Count, e.Enc, e.Independent)
	}

	// Sequential decode of the whole buffer.
	got, decIdx, errs := streamcol.DecodeAll(ctx, data, nil)
	check(len(errs) == 0, "decode errors: %v", errs)
	check(len(got) == len(vals), "decoded %d values, want %d", len(got), len(vals))
	for i := range got {
		check(got[i] == vals[i], "value %d mismatch", i)
	}
	check(len(decIdx) == len(idx), "decoder index has %d blocks, want %d", len(decIdx), len(idx))
	fmt.Println("sequential decode: OK")

	// Random access must match sequential decode.
	for _, k := range []uint64{0, 1, 255, 256, 1234, 4999} {
		v, err := streamcol.DecodeAt(data, decIdx, k)
		check(err == nil, "DecodeAt(%d): %v", k, err)
		check(v == vals[k], "DecodeAt(%d)=%d, want %d", k, v, vals[k])
		fmt.Printf("  random access v[%d] = %d (matches sequential)\n", k, v)
	}

	// Feeding one byte at a time must give the same result.
	d := streamcol.NewDecoder(nil)
	n := 0
	for i := 0; i < len(data); i++ {
		d.Feed(data[i : i+1])
		for {
			vs, err := d.NextBlock(ctx)
			if errors.Is(err, streamcol.ErrNeedMoreData) {
				break
			}
			check(err == nil, "byte-by-byte decode: %v", err)
			n += len(vs)
		}
	}
	check(n == len(vals), "byte-by-byte decoded %d values, want %d", n, len(vals))
	fmt.Println("byte-by-byte streaming decode: OK")

	// Checkpoint after some blocks, resume in a fresh decoder.
	d1 := streamcol.NewDecoder(nil)
	d1.Feed(data)
	total := 0
	for i := 0; i < 3; i++ {
		vs, err := d1.NextBlock(ctx)
		check(err == nil, "pre-checkpoint decode: %v", err)
		total += len(vs)
	}
	cp := d1.Checkpoint()
	d2 := streamcol.ResumeDecoder(cp, nil)
	d2.Feed(data[cp.Offset:])
	tail, _, errs := streamcol.Drain(ctx, d2)
	check(len(errs) == 0, "post-resume decode: %v", errs)
	check(total+len(tail) == len(vals), "resumed total %d, want %d", total+len(tail), len(vals))
	fmt.Printf("checkpoint at offset %d, resumed decoder: OK\n", cp.Offset)

	// Corrupt one dependent block: it is skipped, the blocks depending on it
	// are marked undecodable, and the next independent block recovers.
	bad := bytes.Clone(data)
	bad[idx[1].Offset+20] ^= 0x01
	got2, _, errs2 := streamcol.DecodeAll(ctx, bad, nil)
	fmt.Printf("after corrupting block 1: recovered %d/%d values, %d block errors:\n",
		len(got2), len(vals), len(errs2))
	for _, e := range errs2 {
		fmt.Printf("  %v\n", e)
	}
	check(len(got2) == len(vals)-3*256, "recovered %d values, want %d", len(got2), len(vals)-3*256)
	fmt.Println("all checks passed")
}
