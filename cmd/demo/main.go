package main

import (
	"fmt"
	"strings"
	"unsafe"

	"slaballoc"
)

func main() {
	fmt.Printf("layout: Handle=%dB (page:16|slot:24|gen:24), slotEntry=16B/obj, extent=8B/fragment\n\n",
		unsafe.Sizeof(slaballoc.Handle(0)))

	a := slaballoc.New(256, 1) // one fixed 256-byte page
	sizes := map[slaballoc.Handle]int{}

	put := func(s string) slaballoc.Handle {
		h, err := a.Alloc(len(s))
		if err != nil {
			panic(err)
		}
		copy(must(a.Get(h)), s)
		sizes[h] = len(s)
		return h
	}
	get := func(h slaballoc.Handle) string {
		return string(must(a.Get(h))[:sizes[h]])
	}
	stats := func(tag string) {
		st := a.PageStats(0)
		fmt.Printf("%-24s live=%3dB free=%3dB extents=%d slots=%d\n",
			tag, st.LiveBytes, st.FreeBytes, st.FreeExtents, st.Slots)
	}

	mk := func(c byte) string { return strings.Repeat(string(c), 60) } // 60B -> 64B aligned
	ha := put(mk('A'))
	hb := put(mk('B'))
	hc := put(mk('C'))
	hd := put(mk('D'))
	stats("4x60B allocs (page full)")
	fmt.Printf("handles: a=%v b=%v c=%v d=%v\n", ha, hb, hc, hd)

	a.Free(hb)
	a.Free(hd)
	stats("free b,d (fragmented)")

	offC, _ := a.Offset(hc)
	he, err := a.Alloc(96) // 128B free in two 64B holes -> compaction
	if err != nil {
		panic(err)
	}
	stats("alloc 96B (compacted)")
	offC2, _ := a.Offset(hc)
	fmt.Printf("c moved offset %d -> %d; handle %v still reads %q\n", offC, offC2, hc, get(hc))
	fmt.Printf("a still reads %q; e=%v\n", get(ha), he)

	if _, err := a.Alloc(4096); err != nil {
		fmt.Printf("oversize alloc rejected: %v\n", err)
	}
	stats("after failed alloc")

	a.Free(hc)
	hf := put("xy") // reuses c's slot
	fmt.Printf("slot reuse: old c=%v stale=%v; new f=%v reads %q\n",
		hc, isStale(a, hc), hf, get(hf))
}

func must(b []byte, err error) []byte {
	if err != nil {
		panic(err)
	}
	return b
}

func isStale(a *slaballoc.Allocator, h slaballoc.Handle) bool {
	_, err := a.Get(h)
	return err == slaballoc.ErrStaleHandle
}
