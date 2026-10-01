// Command demo exercises the full stack: hand-rolled wire encoding,
// zero-alloc decoding from pooled objects, and the sharded cache.
package main

import (
	"errors"
	"fmt"

	"protowire/cache"
	"protowire/msg"
	"protowire/pool"
	"protowire/wire"
)

func main() {
	bufs := pool.NewBufferPool()
	c := cache.New(bufs, 16)

	// 1. Build and encode a message with nesting and packed repeats.
	src := msg.AcquireOuter()
	src.Big = -1 << 60
	src.Delta = -123456
	src.F64 = 0x0102030405060708
	src.F32 = 0xAABBCCDD
	src.Label = append(src.Label, "demo-label"...)
	src.Inner = msg.AcquireInner()
	src.Inner.ID = 7
	src.Inner.Name = append(src.Inner.Name, "nested"...)
	src.Nums = append(src.Nums, 1, 127, 128, 300, -5)
	for i := 0; i < 3; i++ {
		it := msg.AcquireInner()
		it.ID = int32(i)
		it.Name = append(it.Name, fmt.Sprintf("item-%d", i)...)
		src.Items = append(src.Items, it)
	}

	w := wire.AcquireWriter()
	msg.EncodeOuter(w, src)
	encoded := append([]byte(nil), w.Bytes()...) // copy out before Release
	w.Release()
	src.Release()
	fmt.Printf("encoded %d bytes: % x\n", len(encoded), encoded)

	// 2. Store in the cache, read back, decode with zero allocations.
	const key = 0xC0FFEE
	c.Put(key, encoded)

	var dst []byte
	raw, ok := c.Get(key, dst[:0])
	if !ok {
		panic("cache miss")
	}
	r := wire.AcquireReader(raw)
	got := msg.AcquireOuter()
	if err := msg.DecodeOuter(r, got); err != nil {
		panic(err)
	}
	r.Release()

	fmt.Printf("decoded: big=%d delta=%d f64=%#x f32=%#x label=%q\n",
		got.Big, got.Delta, got.F64, got.F32, got.Label)
	fmt.Printf("  inner: id=%d name=%q\n", got.Inner.ID, got.Inner.Name)
	fmt.Printf("  packed nums: %v\n", got.Nums)
	for _, it := range got.Items {
		fmt.Printf("  item: id=%d name=%q\n", it.ID, it.Name)
	}
	got.Release()

	// 3. Demonstrate safe truncation handling.
	r = wire.AcquireReader(encoded[:len(encoded)-1])
	bad := msg.AcquireOuter()
	err := msg.DecodeOuter(r, bad)
	bad.Release()
	r.Release()
	fmt.Printf("truncated stream rejected: %v (is ErrTruncated: %v)\n",
		err, errors.Is(err, wire.ErrTruncated))

	// 4. Report pool/cache health: everything balanced, nothing leaked.
	cs := c.Stats()
	fmt.Printf("cache: gets=%d hits=%d entries=%d evictions=%d\n",
		cs.Gets, cs.Hits, cs.Entries, cs.Evictions)
	c.Close()
	ps := bufs.Stats()
	fmt.Printf("buffer pool: acquired=%d released=%d in-use=%d\n",
		ps.Acquired, ps.Released, ps.InUse())
	ms := msg.Stats()
	fmt.Printf("msg pool: outer %d/%d inner %d/%d (acquired/released)\n",
		ms.OuterAcquired, ms.OuterReleased, ms.InnerAcquired, ms.InnerReleased)
}
