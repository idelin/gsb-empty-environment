// Command demo hand-encodes a multi-level nested protobuf message with
// a packed repeated array, parses it with the zero-allocation pooled
// decoder, and prints the decoded fields plus pool statistics.
package main

import (
	"fmt"

	"wirebuf"
)

// buildPayload encodes:
//
//	message Envelope {
//	  uint64 id        = 1;            // varint
//	  sint64 delta     = 2;            // zigzag varint
//	  fixed64 stamp    = 3;
//	  fixed32 flags    = 4;
//	  Inner  inner     = 5;            // nested message
//	  repeated uint32 scores = 6 [packed=true];
//	}
//	message Inner {
//	  string name  = 1;
//	  Core   core  = 2;                // second nesting level
//	}
//	message Core { sint32 level = 1; }
func buildPayload() []byte {
	core := wirebuf.AppendTag(nil, 1, wirebuf.WireVarint)
	core = wirebuf.AppendZigZag32(core, -7)

	inner := wirebuf.AppendTag(nil, 1, wirebuf.WireBytes)
	inner = wirebuf.AppendBytes(inner, []byte("nested-ok"))
	inner = wirebuf.AppendTag(inner, 2, wirebuf.WireBytes)
	inner = wirebuf.AppendBytes(inner, core)

	var packed []byte
	for _, v := range []uint64{3, 1, 4, 1, 5, 9, 2, 6} {
		packed = wirebuf.AppendVarint(packed, v)
	}

	buf := wirebuf.AppendTag(nil, 1, wirebuf.WireVarint)
	buf = wirebuf.AppendVarint(buf, 9001)
	buf = wirebuf.AppendTag(buf, 2, wirebuf.WireVarint)
	buf = wirebuf.AppendZigZag64(buf, -123456)
	buf = wirebuf.AppendTag(buf, 3, wirebuf.WireFixed64)
	buf = wirebuf.AppendFixed64(buf, 0xDEADBEEFCAFEBABE)
	buf = wirebuf.AppendTag(buf, 4, wirebuf.WireFixed32)
	buf = wirebuf.AppendFixed32(buf, 0x7F)
	buf = wirebuf.AppendTag(buf, 5, wirebuf.WireBytes)
	buf = wirebuf.AppendBytes(buf, inner)
	buf = wirebuf.AppendTag(buf, 6, wirebuf.WireBytes)
	buf = wirebuf.AppendBytes(buf, packed)
	return buf
}

func main() {
	payload := buildPayload()
	fmt.Printf("encoded %d bytes of wire-format data\n\n", len(payload))

	// Decode into a pooled message (steady-state: zero heap allocation).
	msg := wirebuf.AcquireMessage()
	if err := wirebuf.Decode(payload, msg); err != nil {
		panic(err)
	}

	fmt.Printf("id    (varint)  = %d\n", msg.Field(1).Varint)
	fmt.Printf("delta (zigzag)  = %d\n", msg.Field(2).ZigZag())
	fmt.Printf("stamp (fixed64) = %#x\n", msg.Field(3).Fixed64)
	fmt.Printf("flags (fixed32) = %#x\n", msg.Field(4).Fixed32)

	inner, err := msg.Field(5).DecodeNested()
	if err != nil {
		panic(err)
	}
	fmt.Printf("inner.name      = %q\n", string(inner.Field(1).Bytes))
	core, err := inner.Field(2).DecodeNested()
	if err != nil {
		panic(err)
	}
	fmt.Printf("inner.core.lvl  = %d\n", core.Field(1).ZigZag())

	scores, err := msg.Field(6).DecodePacked()
	if err != nil {
		panic(err)
	}
	fmt.Printf("scores (packed) = %v\n\n", scores)

	// Zero-allocation cache: decode once, reuse by content hash.
	cache := wirebuf.NewCache(8)
	key := wirebuf.FNV1a64(payload)
	cached := wirebuf.AcquireMessage()
	if err := wirebuf.Decode(payload, cached); err != nil {
		panic(err)
	}
	cache.Put(key, cached)
	hit := cache.Peek(key, func(m *wirebuf.Message) {
		fmt.Printf("cache hit: id=%d fields=%d\n", m.Field(1).Varint, len(m.Fields))
	})
	fmt.Printf("cache hit = %v, entries = %d\n\n", hit, cache.Len())

	// Truncation safety: chop the buffer mid-field.
	truncated := payload[:len(payload)-3]
	probe := wirebuf.AcquireMessage()
	err = wirebuf.Decode(truncated, probe)
	probe.Release()
	fmt.Printf("truncated input -> err = %v\n\n", err)

	// Return everything to the pools, then verify nothing leaked.
	msg.Release()
	cache.Close()
	fmt.Printf("pool balanced (no leaks) = %v\n", wirebuf.Balanced())
}
