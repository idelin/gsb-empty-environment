package main

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"hash/crc32"

	tu "example.com/taggedunion"
)

func main() {
	r := tu.DemoRegistry()

	fmt.Println("== round-trip matrix (encode vX -> migrate -> decode vY) ==")
	for _, from := range []uint16{1, 2} {
		for _, to := range []uint16{1, 2} {
			v := &tu.Value{Tag: tu.TagUser, Version: from, Fields: map[string]any{
				"name": "alice", "email": "a@example.com",
			}}
			if from == 1 {
				v.Fields = map[string]any{"name": "alice"}
			}
			buf, err := r.Encode(v)
			if err != nil {
				fmt.Printf("v%d->v%d: encode error: %v\n", from, to, err)
				continue
			}
			dec, _, err := r.DecodeItem(buf, 0, "item")
			if err != nil {
				fmt.Printf("v%d->v%d: decode error: %v\n", from, to, err)
				continue
			}
			mig, err := r.Migrate(dec, to)
			if err != nil {
				fmt.Printf("v%d->v%d: migrate error: %v\n", from, to, err)
				continue
			}
			fmt.Printf("v%d->v%d: ok fields=%v\n", from, to, mig.Fields)
		}
	}

	fmt.Println("\n== empty branch ==")
	buf, _ := r.Encode(&tu.Value{Tag: tu.TagEmpty, Version: 1, Fields: map[string]any{}})
	dec, _, err := r.DecodeItem(buf, 0, "item")
	fmt.Printf("empty branch: len=%d decoded=%+v err=%v\n", len(buf), dec.Fields, err)

	fmt.Println("\n== unknown tag passthrough ==")
	raw := makeRawItem(777, 5, []byte{0xDE, 0xAD, 0xBE, 0xEF})
	v, _, err := r.DecodeItem(raw, 0, "item")
	re, _ := r.Encode(v)
	fmt.Printf("opaque=%v version=%d preserved=%v byte-identical=%v err=%v\n",
		v.Opaque, v.Version, v.Payload(), bytes.Equal(raw, re), err)

	fmt.Println("\n== tag A carrying tag B fields is rejected ==")
	pointPayload := make([]byte, 8)                    // Point{x:0, y:0} as raw int32 pair
	forged := makeRawItem(tu.TagUser, 1, pointPayload) // User header, Point body
	_, _, err = r.DecodeItem(forged, 0, "forged")
	fmt.Printf("forged item rejected: %v\n", err)

	fmt.Println("\n== atomic batch with error location ==")
	good, _ := r.Encode(&tu.Value{Tag: tu.TagUser, Version: 2, Fields: map[string]any{
		"name": "bob", "email": "b@example.com",
	}})
	batch := make([]byte, 4)
	binary.BigEndian.PutUint32(batch, 3)
	batch = append(batch, good...)
	batch = append(batch, good...)
	bad := make([]byte, len(good))
	copy(bad, good)
	bad[len(bad)-1] ^= 0xFF // corrupt crc of third item
	batch = append(batch, bad...)
	_, err = r.DecodeBatch(batch)
	fmt.Printf("batch rejected atomically: %v\n", err)
}

func makeRawItem(tag, version uint16, payload []byte) []byte {
	item := make([]byte, 8, 8+len(payload)+4)
	binary.BigEndian.PutUint16(item[0:], tag)
	binary.BigEndian.PutUint16(item[2:], version)
	binary.BigEndian.PutUint32(item[4:], uint32(len(payload)))
	item = append(item, payload...)
	var tail [4]byte
	binary.BigEndian.PutUint32(tail[:], crc32.ChecksumIEEE(item))
	return append(item, tail[:]...)
}
