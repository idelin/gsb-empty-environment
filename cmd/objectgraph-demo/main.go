// Command objectgraph-demo exercises the bounded object-graph codec:
//
//  1. encodes/decodes a tree containing a shared node (one entity, many edges),
//  2. encodes/decodes a cyclic graph and verifies pointer identity,
//  3. feeds hostile inputs (oversized fan-out, over-depth chain) and shows
//     they are rejected without leaking partial objects,
//  4. shows the decoder remains usable after the rejected calls.
package main

import (
	"errors"
	"fmt"
	"os"

	"objectgraph/objectgraph"
)

func main() {
	// 1. Tree with sharing.
	b := objectgraph.NewBuilder()
	shared := b.AddString("leaf")
	arr := b.AddArray([]int{shared, shared, shared})
	tree := b.AddObject([]objectgraph.KV{{Key: "items", Ref: arr}})
	p, err := b.Encode(tree)
	if err != nil {
		fail(err)
	}
	v, err := objectgraph.Decode(p)
	if err != nil {
		fail(err)
	}
	items := v.Field("items")
	same := items.Elems()[0] == items.Elems()[1] && items.Elems()[1] == items.Elems()[2]
	fmt.Printf("tree: %d encoded bytes; array len=%d; shared entity=%v\n", len(p), items.Len(), same)

	// 2. Cycle.
	cb := objectgraph.NewBuilder()
	n0 := cb.Declare()
	n1 := cb.Declare()
	if err := cb.SetObject(n0, []objectgraph.KV{{Key: "next", Ref: n1}}); err != nil {
		fail(err)
	}
	if err := cb.SetObject(n1, []objectgraph.KV{{Key: "prev", Ref: n0}}); err != nil {
		fail(err)
	}
	cp, err := cb.Encode(n0)
	if err != nil {
		fail(err)
	}
	cv, err := objectgraph.Decode(cp)
	if err != nil {
		fail(err)
	}
	fmt.Printf("cycle: decoded back-edge returns same root=%v (SCC condensation depth is 1)\n",
		cv.Field("next").Field("prev") == cv)

	// 3. Hostile input against a tight decoder.
	lim := objectgraph.DefaultLimits()
	lim.MaxDepth = 2
	lim.MaxContainerLen = 16
	d, err := objectgraph.NewDecoder(lim)
	if err != nil {
		fail(err)
	}

	deep := objectgraph.NewBuilder()
	x4 := deep.AddNull()
	x3 := deep.AddArray([]int{x4})
	x2 := deep.AddArray([]int{x3})
	x1 := deep.AddArray([]int{x2})
	x0 := deep.AddArray([]int{x1})
	deepBytes, err := deep.Encode(x0)
	if err != nil {
		fail(err)
	}
	if _, derr := d.Decode(deepBytes); errors.Is(derr, objectgraph.ErrDepth) {
		fmt.Printf("hostile: depth-%d chain rejected (ErrDepth), no object returned\n", 5)
	} else {
		fail(fmt.Errorf("expected ErrDepth, got %v", derr))
	}

	// Malicious length-first attack: claim a giant array with no refs.
	fan := encodeGiantFanout()
	if _, derr := d.Decode(fan); errors.Is(derr, objectgraph.ErrBadLength) {
		fmt.Printf("hostile: claimed fan-out rejected before allocation (ErrBadLength)\n")
	} else {
		fail(fmt.Errorf("expected ErrBadLength, got %v", derr))
	}

	// 4. Recovery: same decoder still fully usable.
	gb := objectgraph.NewBuilder()
	gp, err := gb.Encode(gb.AddString("still works"))
	if err != nil {
		fail(err)
	}
	gv, err := d.Decode(gp)
	if err != nil {
		fail(err)
	}
	fmt.Printf("recovery: %q\n", gv.Text())
}

func encodeGiantFanout() []byte {
	// Hand-rolled to guarantee the declared length is huge while the body is
	// absent: the decoder must reject the declared length up front.
	out := []byte{'O', 'G', '1', 1}
	out = appendUvar(out, 1) // nodeCount
	out = appendUvar(out, 0) // root
	out = append(out, 5)     // tagArray
	out = appendUvar(out, 1<<30)
	return out
}

func appendUvar(dst []byte, v uint64) []byte {
	for v >= 0x80 {
		dst = append(dst, byte(v)|0x80)
		v >>= 7
	}
	return append(dst, byte(v))
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "demo error:", err)
	os.Exit(1)
}
