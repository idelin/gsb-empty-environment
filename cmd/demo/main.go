// Command demo hand-encodes a multi-level nested protobuf message with a
// packed repeated field, decodes it with the zero-dependency wire codec,
// demonstrates the safe truncation guard, and runs the integrity inspector
// over a graph containing a dangling reference, an orphan cycle and skew.
package main

import (
	"fmt"
	"math"
	"strings"

	"pbinspect/inspector"
	"pbinspect/wire"
)

type Geo struct {
	Lat float64 `pb:"1,fixed64"`
	Lng float64 `pb:"2,fixed64"`
}

type Address struct {
	City string `pb:"1,bytes"`
	Zip  uint32 `pb:"2,fixed32"`
	Geo  Geo    `pb:"3,bytes"`
}

type Person struct {
	ID     int64    `pb:"1,zigzag"`
	Name   string   `pb:"2,bytes"`
	Addr   *Address `pb:"3,bytes"`
	Scores []int32  `pb:"4,zigzag,packed"`
	Vip    bool     `pb:"5,varint"`
	Stamp  uint64   `pb:"6,fixed64"`
}

func encodePerson() []byte {
	var geo []byte
	geo = wire.AppendTag(geo, 1, wire.WireFixed64)
	geo = wire.AppendFixed64(geo, math.Float64bits(31.2304))
	geo = wire.AppendTag(geo, 2, wire.WireFixed64)
	geo = wire.AppendFixed64(geo, math.Float64bits(121.4737))

	var addr []byte
	addr = wire.AppendTag(addr, 1, wire.WireBytes)
	addr = wire.AppendBytes(addr, []byte("Shanghai"))
	addr = wire.AppendTag(addr, 2, wire.WireFixed32)
	addr = wire.AppendFixed32(addr, 200000)
	addr = wire.AppendTag(addr, 3, wire.WireBytes)
	addr = wire.AppendBytes(addr, geo)

	var packed []byte
	for _, s := range []int32{-3, 0, 7, math.MinInt32, math.MaxInt32} {
		packed = wire.AppendVarint(packed, wire.EncodeZigZag32(s))
	}

	var buf []byte
	buf = wire.AppendTag(buf, 1, wire.WireVarint)
	buf = wire.AppendVarint(buf, wire.EncodeZigZag64(-42))
	buf = wire.AppendTag(buf, 2, wire.WireBytes)
	buf = wire.AppendBytes(buf, []byte("ada"))
	buf = wire.AppendTag(buf, 3, wire.WireBytes)
	buf = wire.AppendBytes(buf, addr)
	buf = wire.AppendTag(buf, 4, wire.WireBytes)
	buf = wire.AppendBytes(buf, packed)
	buf = wire.AppendTag(buf, 5, wire.WireVarint)
	buf = wire.AppendVarint(buf, 1)
	buf = wire.AppendTag(buf, 6, wire.WireFixed64)
	buf = wire.AppendFixed64(buf, 0xdeadbeefcafe)
	return buf
}

func hexDump(b []byte) string {
	var sb strings.Builder
	for i, v := range b {
		if i > 0 && i%16 == 0 {
			sb.WriteString("\n  ")
		} else if i > 0 {
			sb.WriteString(" ")
		}
		fmt.Fprintf(&sb, "%02x", v)
	}
	return sb.String()
}

func main() {
	fmt.Println("=== 1. Hand-encoded protobuf stream (3-level nesting + packed repeated) ===")
	buf := encodePerson()
	fmt.Printf("  %d bytes:\n  %s\n", len(buf), hexDump(buf))

	fmt.Println("\n=== 2. Field-level parse ===")
	fields, err := wire.ParseMessage(buf)
	if err != nil {
		fmt.Println("  parse error:", err)
		return
	}
	for _, f := range fields {
		fmt.Printf("  field %-2d wire=%d", f.Number, f.Type)
		switch f.Type {
		case wire.WireVarint:
			fmt.Printf(" varint=%d", f.Varint)
		case wire.WireFixed32:
			fmt.Printf(" fixed32=%d", f.Fixed32)
		case wire.WireFixed64:
			fmt.Printf(" fixed64=%d", f.Fixed64)
		case wire.WireBytes:
			fmt.Printf(" len=%d", len(f.Bytes))
		}
		fmt.Println()
	}

	fmt.Println("\n=== 3. Reflective unmarshal into struct ===")
	var p Person
	if err := wire.Unmarshal(buf, &p); err != nil {
		fmt.Println("  unmarshal error:", err)
		return
	}
	fmt.Printf("  %+v\n", p)
	fmt.Printf("  addr: %+v\n", *p.Addr)
	fmt.Printf("  geo (level 3): %+v\n", p.Addr.Geo)

	fmt.Println("\n=== 4. Truncation guard (drop last 3 bytes) ===")
	var q Person
	err = wire.Unmarshal(buf[:len(buf)-3], &q)
	fmt.Printf("  decode error (safe, no panic): %v\n", err)
	fmt.Printf("  is ErrTruncated: %v\n", wire.Is(err, wire.ErrTruncated))

	fmt.Println("\n=== 5. Integrity patrol: dangling refs / orphan cycles / skew / invariants ===")
	eng := inspector.NewEngine()
	eng.SetSkewFactor(2.0)
	eng.UpsertNode(inspector.Node{ID: "gateway", Refs: []string{"svc-a", "svc-ghost"}, Weight: 10})
	eng.UpsertNode(inspector.Node{ID: "svc-a", Refs: []string{"db-1"}, Weight: 10})
	eng.UpsertNode(inspector.Node{ID: "db-1", Weight: 1000}) // hot node
	eng.UpsertNode(inspector.Node{ID: "loop-1", Refs: []string{"loop-2"}, Weight: 5})
	eng.UpsertNode(inspector.Node{ID: "loop-2", Refs: []string{"loop-3"}, Weight: 5})
	eng.UpsertNode(inspector.Node{ID: "loop-3", Refs: []string{"loop-1"}, Weight: 5})
	eng.AddRoot("gateway")
	eng.RegisterInvariant("weight-cap", func(s inspector.Snapshot) error {
		for id, n := range s.Nodes {
			if n.Weight > 5000 {
				return fmt.Errorf("node %s exceeds weight cap", id)
			}
		}
		return nil
	})
	rep := eng.Inspect()
	for _, f := range rep.Findings {
		fmt.Printf("  [%-18s] %s\n", f.Kind, f.Detail)
	}
	fmt.Printf("  clean: %v\n", rep.OK())
}
