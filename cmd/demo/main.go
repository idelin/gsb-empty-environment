// Command demo exercises the floatwire library: bit-exact encoding,
// chunked streaming, endianness switching, text display and flagged
// conversions.
package main

import (
	"fmt"

	"floatwire"
)

func main() {
	// 1. Bit-exact encode/decode, including NaN payloads and -0.
	fmt.Println("== bit-exact round trip ==")
	patterns := []uint64{
		0x7FF8DEADBEEFCAFE, // quiet NaN with payload
		0xFFF4000000000001, // negative signaling NaN
		0x8000000000000000, // negative zero
		0x7FF0000000000000, // +Inf
		0x0000000000000001, // smallest subnormal
	}
	for _, p := range patterns {
		enc, err := floatwire.Encode(floatwire.Float64, p)
		if err != nil {
			panic(err)
		}
		dec, err := floatwire.Decode(floatwire.Float64, enc)
		if err != nil {
			panic(err)
		}
		text, _ := floatwire.Format(floatwire.Float64, p)
		fmt.Printf("bits=%016x wire=% x decoded=%016x equal=%v display=%s\n",
			p, enc, dec, floatwire.Equal(floatwire.Float64, p, dec), text)
	}

	// 2. Endianness switching restores bit-level results.
	fmt.Println("== endianness switch ==")
	v := uint64(0x7E55) // float16 qNaN, payload 0x155
	be, _ := floatwire.EncodeOrder(floatwire.Float16, v, floatwire.BigEndian)
	le, _ := floatwire.EncodeOrder(floatwire.Float16, v, floatwire.LittleEndian)
	backBE, _ := floatwire.DecodeOrder(floatwire.Float16, be, floatwire.BigEndian)
	backLE, _ := floatwire.DecodeOrder(floatwire.Float16, le, floatwire.LittleEndian)
	fmt.Printf("bits=%04x big=% x little=% x restored=%04x/%04x\n", v, be, le, backBE, backLE)

	// 3. Arbitrary chunking of a stream.
	fmt.Println("== chunked stream ==")
	enc := floatwire.NewStreamEncoder(floatwire.Float32, floatwire.BigEndian)
	for _, f := range []uint64{0x3F800000, 0x80000000, 0x7FC00001} {
		if err := enc.Write(f); err != nil {
			panic(err)
		}
	}
	stream := enc.Bytes()
	dec := floatwire.NewStreamDecoder(floatwire.Float32, floatwire.BigEndian)
	var got []uint64
	for _, chunk := range [][]byte{stream[:1], stream[1:5], stream[5:6], stream[6:]} {
		out, err := dec.Feed(chunk)
		if err != nil {
			panic(err)
		}
		got = append(got, out...)
	}
	fmt.Printf("stream=% x decoded-in-4-chunks=%08x finish=%v\n", stream, got, dec.Finish())

	// 4. Conversion modes with exception flags.
	fmt.Println("== conversion with flags ==")
	type conv struct {
		from, to floatwire.Width
		bits     uint64
	}
	for _, c := range []conv{
		{floatwire.Float32, floatwire.Float16, 0x3F800000}, // 1.0, exact
		{floatwire.Float32, floatwire.Float16, 0x3F8CCCCD}, // 1.1, inexact
		{floatwire.Float32, floatwire.Float16, 0x7F7FFFFF}, // max f32, overflow
		{floatwire.Float32, floatwire.Float16, 0x358637BD}, // 1e-6, underflow
		{floatwire.Float32, floatwire.Float16, 0x7F800001}, // sNaN, invalid
		{floatwire.Float64, floatwire.Float16, 0x7FF8DEADBEEFCAFE},
	} {
		out, flags, err := floatwire.ConvertFlags(c.from, c.to, c.bits, floatwire.ModeNumeric)
		if err != nil {
			panic(err)
		}
		text, _ := floatwire.Format(c.to, out)
		fmt.Printf("f%d->f%d %0*x -> %0*x flags=%s display=%s\n",
			c.from, c.to, int(c.from)/4, c.bits, int(c.to)/4, out, flags, text)
	}

	// 5. Framed records validate before decoding.
	fmt.Println("== framed records ==")
	frame, _ := floatwire.EncodeFrame(floatwire.Float16, 0x7E55)
	bits, n, err := floatwire.DecodeFrame(frame)
	fmt.Printf("frame=% x bits=%04x consumed=%d err=%v\n", frame, bits, n, err)
	if _, _, err := floatwire.DecodeFrame(frame[:3]); err != nil {
		fmt.Printf("truncated frame rejected: %v\n", err)
	}
	if _, err := floatwire.Encode(floatwire.Width(24), 0); err != nil {
		fmt.Printf("unsupported width rejected: %v\n", err)
	}
}
