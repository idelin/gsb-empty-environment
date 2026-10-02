// pbdemo builds a multi-level nested protobuf stream by hand, decodes it
// through the reflection engine, and prints the aggregated metrics export.
package main

import (
	"fmt"

	"pbwire/metrics"
	"pbwire/wire"
)

// Order mirrors a protobuf schema:
//
//	message Order {
//	  uint64 id            = 1;
//	  sint64 delta         = 2;
//	  string customer      = 3;
//	  Address address      = 4;   // embedded message
//	  repeated int32 scores= 5 [packed=true];
//	  fixed64 checksum     = 6;
//	}
type Address struct {
	City    string `pb:"1,string"`
	Zip     uint32 `pb:"2,varint"`
	GeoHash uint64 `pb:"3,fixed64"`
}

type Order struct {
	ID       uint64   `pb:"1,varint"`
	Delta    int64    `pb:"2,zigzag"`
	Customer string   `pb:"3,string"`
	Address  *Address `pb:"4,msg"`
	Scores   []int32  `pb:"5,packed"`
	Checksum uint64   `pb:"6,fixed64"`
}

func buildOrder() []byte {
	addr := wire.NewEncoder().
		String(1, "Shanghai").
		Varint(2, 200001).
		Fixed64(3, 0x9c04deadbeef)
	return wire.NewEncoder().
		Varint(1, 9001).
		ZigZag(2, -42).
		String(3, "delin").
		Message(4, addr).
		PackedVarints(5,
			wire.EncodeZigZag64(-3), wire.EncodeZigZag64(0), wire.EncodeZigZag64(7)).
		Fixed64(6, 0x0123456789abcdef).
		Bytes()
}

func main() {
	reg := &metrics.Registry{}
	dec := wire.NewMetered(reg)

	stream := buildOrder()
	fmt.Printf("encoded stream: %d bytes\n%x\n\n", len(stream), stream)

	// Decode the intact stream twice (second pass hits the schema cache).
	for i := 0; i < 2; i++ {
		var o Order
		if err := dec.Unmarshal(stream, &o); err != nil {
			fmt.Println("decode error:", err)
			return
		}
		fmt.Printf("decoded: id=%d delta=%d customer=%q city=%s zip=%d scores=%v checksum=%#x\n",
			o.ID, o.Delta, o.Customer, o.Address.City, o.Address.Zip, o.Scores, o.Checksum)
	}

	// Truncated stream: cut inside the embedded Address message.
	truncated := stream[:len(stream)-10]
	var o Order
	if err := dec.Unmarshal(truncated, &o); err != nil {
		fmt.Printf("\ntruncated stream rejected safely: %v\n", err)
	}

	fmt.Println("\n--- metrics export ---")
	fmt.Print(reg.Export())

	snap := reg.Snapshot()
	fmt.Printf("\nschema cache hit rate: %.2f\n", snap.HitRates["schema_cache"])
}
