// Command demo exercises the bitstream package end to end: arbitrary
// widths, independent bit/byte orders, peek, nested mark/rollback,
// zero-copy views, retention water mark stats and framed round trips.
package main

import (
	"fmt"

	"bitstream"
)

func main() {
	cfg := bitstream.Config{Bits: bitstream.MSBFirst, Bytes: bitstream.BigEndian}

	// Write unaligned fields, including a 13-bit cross-byte value.
	w := bitstream.NewWriter(cfg)
	must(w.Write(0b101, 3))
	must(w.Write(0x1ABC, 13))
	must(w.Write(0x2FFFFFFFFFFFFFFF, 62))
	fmt.Printf("written %d bits into %d bytes\n", w.Len(), len(w.Bytes()))

	// Frame it (magic, version, orders, bit length, CRC-32).
	frame, err := bitstream.Marshal(cfg, w.Bytes(), w.Len())
	must(err)
	fmt.Printf("frame: %d bytes, version=%d\n", len(frame), bitstream.FormatVersion)

	// Decode with a bounded output budget.
	gotCfg, payload, nbits, err := bitstream.Unmarshal(frame, 1<<20)
	must(err)
	fmt.Printf("decoded header: cfg=%+v payload=%d bits\n", gotCfg, nbits)

	// Split the payload into two blocks to prove cross-block zero-copy.
	c := &bitstream.Chain{}
	half := len(payload) / 2
	c.AppendBytes(payload[:half], half*8)
	c.AppendBytes(payload[half:], nbits-half*8)

	r := bitstream.NewReader(gotCfg, c)
	r.SetQuota(1 << 16)

	peeked, err := r.Peek(3)
	must(err)
	fmt.Printf("peek(3)=%#b pos=%d (cursor unmoved)\n", peeked, r.Pos())

	r.Mark()
	v3, err := r.Read(3)
	must(err)
	v13, err := r.Read(13)
	must(err)
	fmt.Printf("read: prefix=%#b value13=%#04x pos=%d\n", v3, v13, r.Pos())
	must(r.Rollback())
	fmt.Printf("after rollback pos=%d\n", r.Pos())

	r.Mark()
	must(func() error { _, e := r.Read(3); return e }())
	view, err := r.View(13)
	must(err)
	vr := view.Reader(gotCfg)
	vv, err := vr.Read(13)
	must(err)
	fmt.Printf("zero-copy view across blocks: %#04x\n", vv)
	view.Close()
	must(r.Commit())

	stats := bitstream.BufferStats()
	fmt.Printf("buffer stats: live=%d blocks/%d bytes reclaimed=%d bytes\n",
		stats.LiveBlocks, stats.LiveBytes, stats.Reclaimed)
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}
