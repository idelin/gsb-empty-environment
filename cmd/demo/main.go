// Command demo hand-builds a multi-level nested protobuf binary stream
// (including packed repeated arrays), decodes it with the reflection
// engine, demonstrates the truncation guard, and runs the offline
// integrity inspector over a snapshot graph.
package main

import (
	"fmt"

	"wireinspect/inspector"
	"wireinspect/wire"
)

type Node struct {
	Name   string   `pb:"1,bytes"`
	Score  int64    `pb:"2,zigzag"`
	Values []uint64 `pb:"3,varint"` // packed repeated
	Next   *Node    `pb:"4,bytes"`
}

type Envelope struct {
	ID     uint64  `pb:"1,varint"`
	Title  string  `pb:"2,bytes"`
	Deltas []int32 `pb:"3,zigzag"` // packed repeated
	Child  *Node   `pb:"4,bytes"`
	Fixed  uint32  `pb:"5,fixed32"`
	Big    uint64  `pb:"6,fixed64"`
	Active bool    `pb:"7,varint"`
}

func encodeNode(n *Node) []byte {
	var buf []byte
	buf = wire.AppendTag(buf, 1, wire.BytesType)
	buf = wire.AppendBytes(buf, []byte(n.Name))
	buf = wire.AppendTag(buf, 2, wire.VarintType)
	buf = wire.AppendZigZag64(buf, n.Score)
	if len(n.Values) > 0 {
		var packed []byte
		for _, v := range n.Values {
			packed = wire.AppendVarint(packed, v)
		}
		buf = wire.AppendTag(buf, 3, wire.BytesType)
		buf = wire.AppendBytes(buf, packed)
	}
	if n.Next != nil {
		buf = wire.AppendTag(buf, 4, wire.BytesType)
		buf = wire.AppendBytes(buf, encodeNode(n.Next))
	}
	return buf
}

func encodeEnvelope(e *Envelope) []byte {
	var buf []byte
	buf = wire.AppendTag(buf, 1, wire.VarintType)
	buf = wire.AppendVarint(buf, e.ID)
	buf = wire.AppendTag(buf, 2, wire.BytesType)
	buf = wire.AppendBytes(buf, []byte(e.Title))
	var packed []byte
	for _, d := range e.Deltas {
		packed = wire.AppendZigZag32(packed, d)
	}
	buf = wire.AppendTag(buf, 3, wire.BytesType)
	buf = wire.AppendBytes(buf, packed)
	buf = wire.AppendTag(buf, 4, wire.BytesType)
	buf = wire.AppendBytes(buf, encodeNode(e.Child))
	buf = wire.AppendTag(buf, 5, wire.Fixed32Type)
	buf = wire.AppendFixed32(buf, e.Fixed)
	buf = wire.AppendTag(buf, 6, wire.Fixed64Type)
	buf = wire.AppendFixed64(buf, e.Big)
	buf = wire.AppendTag(buf, 7, wire.VarintType)
	buf = wire.AppendVarint(buf, 1)
	return buf
}

func main() {
	fmt.Println("=== 1. Hand-built nested protobuf stream ===")
	src := &Envelope{
		ID:     9001,
		Title:  "telemetry-batch",
		Deltas: []int32{-3, 0, 7, -128, 4096},
		Child: &Node{
			Name:   "leaf",
			Score:  -8192,
			Values: []uint64{1, 300, 65536, 1<<60 + 7},
			Next:   &Node{Name: "root-of-leaf", Score: 42},
		},
		Fixed:  0xDEADBEEF,
		Big:    0x0123456789ABCDEF,
		Active: true,
	}
	blob := encodeEnvelope(src)
	fmt.Printf("encoded %d bytes: % x\n", len(blob), blob)

	var dst Envelope
	if err := wire.Unmarshal(blob, &dst); err != nil {
		fmt.Println("decode error:", err)
		return
	}
	fmt.Printf("decoded: ID=%d Title=%q Deltas=%v Fixed=%#08x Big=%#016x Active=%v\n",
		dst.ID, dst.Title, dst.Deltas, dst.Fixed, dst.Big, dst.Active)
	fmt.Printf("nested:  %+v -> %+v\n", dst.Child, dst.Child.Next)

	fmt.Println("\n=== 2. Truncation guard ===")
	for _, cut := range []int{1, 5, len(blob) / 2, len(blob) - 1} {
		var victim Envelope
		err := wire.Unmarshal(blob[:cut], &victim)
		fmt.Printf("truncated to %2d/%d bytes -> %v\n", cut, len(blob), err)
	}

	fmt.Println("\n=== 3. Offline integrity inspection ===")
	snapshot := []inspector.Record{
		{ID: 1, Refs: []uint64{2, 3}, Weight: 10, Root: true},
		{ID: 2, Refs: []uint64{3}, Weight: 12},
		{ID: 3, Refs: []uint64{99}, Weight: 400}, // dangling ref + skew outlier
		{ID: 4, Refs: []uint64{5}, Weight: 8},    // orphan cycle 4<->5
		{ID: 5, Refs: []uint64{4}, Weight: 9},
		{ID: 6, Weight: 7}, // unreachable, acyclic
	}
	rep := inspector.Inspect(snapshot, inspector.Options{SkewThreshold: 3.0})
	fmt.Printf("nodes=%d edges=%d reachable=%d cyclicGroups=%d skewRatio=%.2f ok=%v\n",
		rep.Nodes, rep.Edges, rep.Reachable, rep.CyclicGroups, rep.SkewRatio, rep.OK())
	for _, issue := range rep.Issues {
		fmt.Println(" ", issue)
	}
	fmt.Println("\n=== 4. Invariant assertion ===")
	err := inspector.Assert(rep, inspector.StandardInvariants...)
	fmt.Println("assert(StandardInvariants):", err)
	clean := inspector.Inspect([]inspector.Record{
		{ID: 1, Refs: []uint64{2}, Weight: 10, Root: true},
		{ID: 2, Weight: 10},
	}, inspector.Options{})
	fmt.Println("assert(clean graph):", inspector.Assert(clean,
		inspector.NoDanglingRefs, inspector.AllReachable, inspector.SkewWithin(3.0)))
}
