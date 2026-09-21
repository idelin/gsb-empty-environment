// Command demo exercises the seqwin receiver and the incremental frame
// parser with printable output.
package main

import (
	"fmt"

	"seqwin"
)

func receiverDemo() {
	fmt.Println("== window receiver (8-bit space, window 8, frontier starts at 254) ==")
	sp, _ := seqwin.NewSpace(8)
	r, err := seqwin.NewReceiverAt(sp, 8, 254)
	if err != nil {
		panic(err)
	}
	stream := []uint64{254, 255, 0, 1, 3, 3, 2, 250, 132, 20, 4, 5, 6, 7, 8, 9}
	for _, s := range stream {
		v := r.Deliver(s)
		fmt.Printf("  deliver %3d -> %-17v next=%3d acked=%d ranges=%v\n",
			s, v, r.Next(), r.Acked(), r.AckRanges())
	}
	if cum, ok := r.Cumulative(); ok {
		fmt.Printf("  cumulative ack: %d (distinct acked: %d)\n", cum, r.Acked())
	}
	fmt.Println("  note: 250 is too old, 132 is exactly half-space away (ambiguous), 20 is beyond the window")
}

func parserDemo() {
	fmt.Println("== incremental frame parser (quota 64, fed in 3-byte chunks) ==")
	f1, _ := seqwin.EncodeFrame(1, 0, []byte("hello"), 64)
	f2, _ := seqwin.EncodeFrame(2, 0, []byte("wrap-around"), 64)
	wire := append(f1, f2...)
	p := seqwin.NewParser(64)
	for i := 0; i < len(wire); i += 3 {
		end := i + 3
		if end > len(wire) {
			end = len(wire)
		}
		frames, err := p.Feed(wire[i:end])
		if err != nil {
			fmt.Printf("  chunk [%d:%d]: error %v\n", i, end, err)
			continue
		}
		for _, f := range frames {
			fmt.Printf("  chunk [%d:%d]: frame type=%d payload=%q\n", i, end, f.Type, f.Payload)
		}
	}

	fmt.Println("== corrupt vs incomplete ==")
	bad, _ := seqwin.EncodeFrame(1, 0, []byte("tampered"), 64)
	bad[7] ^= 0xFF // flip a payload bit
	if _, err := seqwin.NewParser(64).Feed(bad); seqwin.IsCorrupt(err) {
		fmt.Printf("  tampered frame: %v\n", err)
	}
	hdr := []byte{0x5A, 0xA5, 1, 0, 0xFF, 0xFF} // declares 65535-byte payload
	if _, err := seqwin.NewParser(64).Feed(hdr); seqwin.IsCorrupt(err) {
		fmt.Printf("  over-quota header rejected before allocation: %v\n", err)
	}
	p2 := seqwin.NewParser(64)
	if _, err := p2.Feed(hdr[:4]); err == nil {
		fmt.Println("  4-byte prefix: no error, parser waits for more data (incomplete, not corrupt)")
	}
}

func main() {
	receiverDemo()
	fmt.Println()
	parserDemo()
}
