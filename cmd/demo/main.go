// Demo: convert a record between big-endian and little-endian per a
// declared schema, preserving NaN bit patterns, then decode a chunked
// stream.
package main

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"

	"endianconv"
)

func main() {
	schema := endianconv.Schema{Fields: []endianconv.Field{
		endianconv.U16(),             // message type
		endianconv.F64(),             // sensor value (may be NaN)
		endianconv.VarBytes(2, 1024), // 2-byte length header + payload
		endianconv.U32(),             // checksum
	}}

	// A signaling NaN with a payload — must survive conversion bit-exact.
	nan := math.Float64frombits(0x7FF0000000000042)
	payload := []byte{0xDE, 0xAD, 0xBE, 0xEF}

	// Build a big-endian record: type=7, value=NaN, payload, checksum.
	be := []byte{0x00, 0x07}
	var f [8]byte
	binary.BigEndian.PutUint64(f[:], math.Float64bits(nan))
	be = append(be, f[:]...)
	be = append(be, 0x00, 0x04) // length header: 4
	be = append(be, payload...)
	be = append(be, 0xCA, 0xFE, 0xBA, 0xBE)

	fmt.Printf("big-endian record:    %x\n", be)

	// Convert to little-endian, in place.
	if _, err := schema.Convert(be, be, binary.BigEndian, binary.LittleEndian, 0); err != nil {
		panic(err)
	}
	fmt.Printf("little-endian record: %x\n", be)
	fmt.Printf("  type=%d value-bits=%#x payload=%x checksum=%#x\n",
		binary.LittleEndian.Uint16(be[0:2]),
		binary.LittleEndian.Uint64(be[2:10]),
		be[12:12+len(payload)],
		binary.LittleEndian.Uint32(be[12+len(payload):]))

	// Convert back; every byte must be restored.
	back, err := schema.ConvertRecord(be, binary.LittleEndian, binary.BigEndian, 0)
	if err != nil {
		panic(err)
	}
	fmt.Printf("round trip ok:        %v (NaN bits %#x preserved)\n",
		string(back) == string([]byte{0x00, 0x07, f[0], f[1], f[2], f[3], f[4], f[5], f[6], f[7], 0x00, 0x04, 0xDE, 0xAD, 0xBE, 0xEF, 0xCA, 0xFE, 0xBA, 0xBE}),
		math.Float64bits(math.Float64frombits(binary.BigEndian.Uint64(back[2:10]))))

	// Stream decoding: feed the record one byte at a time.
	dec := endianconv.NewDecoder(schema, binary.LittleEndian, 4096)
	for i, b := range be {
		dec.Feed([]byte{b})
		rec, err := dec.Next()
		if errors.Is(err, endianconv.ErrNeedMoreData) {
			continue
		}
		if err != nil {
			panic(err)
		}
		fmt.Printf("stream decoded after %d bytes: %x\n", i+1, rec)
	}

	// Bad data: a length header of 65535 exceeds the 1024-byte quota and
	// is rejected immediately, before the payload ever arrives.
	bad := endianconv.NewDecoder(schema, binary.LittleEndian, 4096)
	bad.Feed([]byte{0x07, 0x00, 0, 0, 0, 0, 0, 0, 0, 0, 0xFF, 0xFF})
	_, err = bad.Next()
	fmt.Printf("oversize header:      %v\n", err)
}
