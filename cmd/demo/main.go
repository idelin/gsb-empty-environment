// Demo: encode a two-channel stream, dump the wire bytes, then decode it
// in awkward 3-byte chunks to show chunked parsing, missing samples,
// reset, periodic anchors, and corruption handling.
package main

import (
	"bytes"
	"encoding/hex"
	"fmt"

	"example.com/deltastream/deltacodec"
)

func main() {
	var buf bytes.Buffer
	enc := deltacodec.NewEncoder(&buf, 4) // full anchor every 4 samples

	enc.Sample(1, 100) // anchor 100
	enc.Sample(1, 107) // delta +7
	enc.Sample(1, 95)  // delta -12 (negative)
	enc.Missing(1)     // gap: baseline kept
	enc.Sample(1, 99)  // delta +4 from 95
	enc.Sample(2, -5)  // ch2 is independent
	enc.Reset(1)       // baseline cleared
	enc.Sample(1, 500) // encoder re-anchors

	stream := buf.Bytes()
	fmt.Printf("wire (%d bytes): %s\n\n", len(stream), hex.EncodeToString(stream))

	dec := deltacodec.NewDecoder()
	for i := 0; i < len(stream); i += 3 {
		end := i + 3
		if end > len(stream) {
			end = len(stream)
		}
		events, err := dec.Write(stream[i:end])
		if err != nil {
			fmt.Println("fatal stream error:", err)
			break
		}
		for _, e := range events {
			fmt.Println(" ", e)
		}
	}

	fmt.Println("\n-- now corrupt one delta frame and watch the poison window --")
	buf.Reset()
	deltacodec.EncodeAnchor(&buf, 1, 50)
	deltacodec.EncodeDelta(&buf, 1, 1) // 51
	stream = append([]byte(nil), buf.Bytes()...)
	stream = append(stream, 3, 0x02, 1, 0x80) // delta frame with truncated varint
	buf.Reset()
	deltacodec.EncodeDelta(&buf, 1, 1) // undecodable: a delta was lost
	deltacodec.EncodeDelta(&buf, 1, 1) // undecodable
	deltacodec.EncodeAnchor(&buf, 1, 900)
	deltacodec.EncodeDelta(&buf, 1, 5) // 905
	stream = append(stream, buf.Bytes()...)

	events, err := deltacodec.NewDecoder().Write(stream)
	if err != nil {
		fmt.Println("fatal stream error:", err)
	}
	for _, e := range events {
		fmt.Println(" ", e)
	}
}
