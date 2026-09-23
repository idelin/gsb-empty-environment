package objectgraph_test

import (
	"errors"
	"fmt"

	"objectgraph/objectgraph"
)

// ExampleDecode_roundTrip encodes and decodes an array/object tree with a
// shared node, showing that sharing is preserved as a single entity.
func ExampleDecode_roundTrip() {
	b := objectgraph.NewBuilder()
	word := b.AddString("shared")
	arr := b.AddArray([]int{word, word})
	root := b.AddObject([]objectgraph.KV{{Key: "items", Ref: arr}})

	p, err := b.Encode(root)
	if err != nil {
		panic(err)
	}
	v, err := objectgraph.Decode(p)
	if err != nil {
		panic(err)
	}
	items := v.Field("items")
	fmt.Println(items.Kind(), items.Len(), items.Elems()[0] == items.Elems()[1])
	// Output: array 2 true
}

// ExampleDecode_cycle builds and decodes a cyclic graph. Depth is measured on
// the SCC condensation, so the cycle is depth 1 rather than infinite.
func ExampleDecode_cycle() {
	b := objectgraph.NewBuilder()
	n0 := b.Declare()
	n1 := b.Declare()
	if err := b.SetObject(n0, []objectgraph.KV{{Key: "next", Ref: n1}}); err != nil {
		panic(err)
	}
	if err := b.SetObject(n1, []objectgraph.KV{{Key: "prev", Ref: n0}}); err != nil {
		panic(err)
	}
	p, err := b.Encode(n0)
	if err != nil {
		panic(err)
	}
	v, err := objectgraph.Decode(p)
	if err != nil {
		panic(err)
	}
	fmt.Println(v.Field("next").Field("prev") == v)
	// Output: true
}

// ExampleDecode_rejected shows that an over-limit payload fails without
// returning a partial object, and that a later call works normally.
func ExampleDecode_rejected() {
	lim := objectgraph.DefaultLimits()
	lim.MaxDepth = 2
	d, err := objectgraph.NewDecoder(lim)
	if err != nil {
		panic(err)
	}

	deep := objectgraph.NewBuilder()
	n4 := deep.AddNull()
	n3 := deep.AddArray([]int{n4})
	n2 := deep.AddArray([]int{n3})
	n1 := deep.AddArray([]int{n2})
	n0 := deep.AddArray([]int{n1})
	bad, err := deep.Encode(n0)
	if err != nil {
		panic(err)
	}
	if _, err := d.Decode(bad); !errors.Is(err, objectgraph.ErrDepth) {
		panic("expected ErrDepth")
	}
	fmt.Println("rejected")

	good := objectgraph.NewBuilder()
	ok, err := good.Encode(good.AddString("fine"))
	if err != nil {
		panic(err)
	}
	v, err := d.Decode(ok)
	if err != nil {
		panic(err)
	}
	fmt.Println(v.Text())
	// Output:
	// rejected
	// fine
}
