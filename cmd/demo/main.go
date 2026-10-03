// Command demo hand-crafts a protobuf wire stream (nested messages +
// packed repeated fields), parses it with the zero-dependency wire
// engine, then exercises the adaptive sparse container and its
// compressed snapshot format.
package main

import (
	"fmt"
	"math/rand"

	"wireopt/sparse"
	"wireopt/wire"
)

func buildPerson() []byte {
	// address { city_id: 310110, zip: 200001 }
	var addr []byte
	addr = wire.AppendTag(addr, 1, wire.Varint)
	addr = wire.AppendVarint(addr, 310110)
	addr = wire.AppendTag(addr, 2, wire.Varint)
	addr = wire.AppendVarint(addr, 200001)

	var buf []byte
	buf = wire.AppendTag(buf, 1, wire.Varint) // id
	buf = wire.AppendVarint(buf, 1001)
	buf = wire.AppendTag(buf, 2, wire.Varint) // balance (sint64)
	buf = wire.AppendVarint(buf, wire.ZigzagEncode(-4096))
	buf = wire.AppendTag(buf, 3, wire.Bytes) // name
	buf = wire.AppendVarint(buf, 5)
	buf = append(buf, "alice"...)
	buf = wire.AppendTag(buf, 4, wire.Bytes) // address (nested)
	buf = wire.AppendVarint(buf, uint64(len(addr)))
	buf = append(buf, addr...)
	var scores []byte // packed repeated uint32
	for _, s := range []uint64{98, 100, 300, 65536} {
		scores = wire.AppendVarint(scores, s)
	}
	buf = wire.AppendTag(buf, 5, wire.Bytes)
	buf = wire.AppendVarint(buf, uint64(len(scores)))
	buf = append(buf, scores...)
	return buf
}

func main() {
	stream := buildPerson()
	fmt.Printf("hand-crafted protobuf stream: %d bytes\n%x\n\n", len(stream), stream)

	d := wire.NewDecoder()
	m, err := d.Parse(stream)
	if err != nil {
		panic(err)
	}
	id, _ := m.Last(1)
	balance, _ := m.Last(2)
	name, _ := m.Last(3)
	addrV, _ := m.Last(4)
	scoresV, _ := m.Last(5)
	fmt.Printf("id      = %d\n", id.Num)
	fmt.Printf("balance = %d (zigzag)\n", balance.AsZigzag())
	fmt.Printf("name    = %s\n", name.Bytes)
	addr, err := d.Nested(addrV, 0)
	if err != nil {
		panic(err)
	}
	city, _ := addr.Last(1)
	zip, _ := addr.Last(2)
	fmt.Printf("address = { city_id: %d, zip: %d } (nested depth 1)\n", city.Num, zip.Num)
	scores, err := wire.DecodePackedVarint(scoresV.Bytes)
	if err != nil {
		panic(err)
	}
	fmt.Printf("scores  = %v (packed repeated)\n\n", scores)

	// Truncation safety demo: cut the stream mid-field.
	if _, err := d.Parse(stream[:len(stream)-2]); err != nil {
		fmt.Printf("truncated stream safely rejected: %v\n\n", err)
	}

	// Sparse optimizer demo: 1M-wide index space, 5k live entries.
	a := sparse.New()
	rng := rand.New(rand.NewSource(7))
	for i := 0; i < 5000; i++ {
		a.Set(uint32(rng.Intn(1<<20)), uint64(rng.Intn(1000)))
	}
	blob := a.Compress()
	fmt.Printf("sparse: %d live entries over span %d, mode dense=%v\n", a.Len(), a.Span(), a.Dense())
	fmt.Printf("sparse: est. mem %d bytes, compressed snapshot %d bytes (%.1f bytes/entry)\n",
		a.MemBytes(), len(blob), float64(len(blob))/float64(a.Len()))
	back, err := sparse.Decompress(blob)
	if err != nil {
		panic(err)
	}
	fmt.Printf("sparse: snapshot round-trip restored %d entries\n\n", back.Len())

	// Dense regime demo: fill a contiguous range, watch it convert.
	b := sparse.New()
	for i := uint32(0); i < 10000; i++ {
		b.Set(i, uint64(i))
	}
	fmt.Printf("dense regime: %d entries, dense=%v, est. mem %d bytes (map would cost ~%d)\n",
		b.Len(), b.Dense(), b.MemBytes(), b.Len()*48)
}
