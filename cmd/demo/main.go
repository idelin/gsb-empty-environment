// Command demo converts one big-endian record to little-endian and back,
// including a NaN-bearing float and a variable-length payload, and also
// shows byte-at-a-time streaming with the parser.
package main

import (
	"encoding/binary"
	"fmt"

	"endconv/endconv"
)

func hex(b []byte) string {
	s := ""
	for _, x := range b {
		s += fmt.Sprintf("%02x ", x)
	}
	return s
}

func main() {
	// Schema: u16 id, f64 sensor, u8 flags, VarBlob tag, u32 checksum.
	schema := endconv.Schema{
		{Kind: endconv.U16},
		{Kind: endconv.F64},
		{Kind: endconv.U8},
		{Kind: endconv.VarBlob},
		{Kind: endconv.U32},
	}

	// f64 bits encode a NaN with a non-trivial payload: 0x7ff8_0000_0000_0100.
	nanBits := uint64(0x7ff8000000000100)

	rec := make([]byte, 0, 32)
	rec = binary.BigEndian.AppendUint16(rec, 0x0102)     // u16
	rec = binary.BigEndian.AppendUint64(rec, nanBits)    // f64 (NaN + payload)
	rec = append(rec, 0x7f)                              // u8
	rec = binary.BigEndian.AppendUint16(rec, 4)          // VarBlob header
	rec = append(rec, 0xde, 0xad, 0xbe, 0xef)            // VarBlob payload
	rec = binary.BigEndian.AppendUint32(rec, 0x11223344) // u32

	original := append([]byte(nil), rec...)
	fmt.Printf("BE record: %s\n", hex(rec))

	if err := endconv.ConvertInPlace(rec, schema, endconv.BigToLittle); err != nil {
		panic(err)
	}
	fmt.Printf("LE record: %s\n", hex(rec))

	// The NaN payload bits must survive endian conversion untouched.
	roundTripNaN := binary.LittleEndian.Uint64(rec[2:10])
	fmt.Printf("LE f64 bits: %016x (NaN payload preserved: %v)\n",
		roundTripNaN, roundTripNaN == nanBits)
	fmt.Printf("payload bytes unchanged: %v\n",
		hex(rec[13:17]) == "de ad be ef ")

	if err := endconv.ConvertInPlace(rec, schema, endconv.LittleToBig); err != nil {
		panic(err)
	}
	fmt.Printf("BE again : %s\n", hex(rec))
	fmt.Printf("double conversion restores every byte: %v\n", equal(rec, original))

	// Streaming: feed two records one byte at a time.
	p, err := endconv.NewParser(schema, endconv.BigToLittle, endconv.Limits{
		MaxRecordBytes: 1024, MaxPayloadBytes: 256,
	})
	if err != nil {
		panic(err)
	}
	second := append([]byte(nil), original...)
	stream := append(append([]byte(nil), original...), second...)

	got := 0
	for i, oneByte := range stream {
		if err := p.Write([]byte{oneByte}); err != nil {
			panic(err)
		}
		out, status, err := p.Next()
		if err != nil {
			panic(err)
		}
		switch status {
		case endconv.NeedMoreData:
			// expected until each record completes
		case endconv.RecordReady:
			got++
			fmt.Printf("stream emitted record %d after byte %d/%d: %s\n",
				got, i+1, len(stream), hex(out))
		}
	}
	fmt.Printf("streaming records delivered: %d\n", got)
}

func equal(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
