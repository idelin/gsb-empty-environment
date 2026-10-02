// Command demo wires the three components together:
//  1. hand-encodes a multi-level nested protobuf stream (wirefmt),
//  2. parses it back into structs and validates them (validate),
//  3. routes keys through the hierarchical sharding router (router).
package main

import (
	"fmt"

	"pairwise/router"
	"pairwise/validate"
	"pairwise/wirefmt"
)

// NodeInfo mirrors: message NodeInfo { string id=1; string region=2;
// string zone=3; sint32 load=4; }
type NodeInfo struct {
	ID     string `validate:"required"`
	Region string `validate:"required,min=2"`
	Zone   string `validate:"required"`
	Load   int32  `validate:"min=-100,max=100"`
}

// Heartbeat mirrors: message Heartbeat { uint64 seq=1; NodeInfo node=2;
// repeated sint32 latencies=3 [packed]; repeated fixed64 checksums=4
// [packed]; repeated string tags=5; }
type Heartbeat struct {
	Seq       uint64   `validate:"min=1"`
	Node      NodeInfo `validate:"required"`
	Latencies []int32  `validate:"max=8"`
	Checksums []uint64
	Tags      []string `validate:"max=4"`
}

func encodeHeartbeat(h *Heartbeat) []byte {
	e := wirefmt.NewEncoder()
	e.AppendUint64(1, h.Seq)
	e.AppendMessage(2, func(sub *wirefmt.Encoder) {
		sub.AppendString(1, h.Node.ID)
		sub.AppendString(2, h.Node.Region)
		sub.AppendString(3, h.Node.Zone)
		sub.AppendSint32(4, h.Node.Load)
	})
	e.AppendPackedSint32s(3, h.Latencies)
	e.AppendPackedFixed64s(4, h.Checksums)
	for _, tag := range h.Tags {
		e.AppendString(5, tag)
	}
	return e.Bytes()
}

func decodeHeartbeat(buf []byte) (*Heartbeat, error) {
	h := &Heartbeat{}
	r := wirefmt.NewReader(buf)
	for !r.EOF() {
		fn, wt, err := r.ReadTag()
		if err != nil {
			return nil, err
		}
		switch fn {
		case 1:
			if h.Seq, err = r.ReadVarint(); err != nil {
				return nil, err
			}
		case 2:
			sub, err := r.ReadMessage()
			if err != nil {
				return nil, err
			}
			for !sub.EOF() {
				sfn, swt, err := sub.ReadTag()
				if err != nil {
					return nil, err
				}
				switch sfn {
				case 1:
					if h.Node.ID, err = sub.ReadString(); err != nil {
						return nil, err
					}
				case 2:
					if h.Node.Region, err = sub.ReadString(); err != nil {
						return nil, err
					}
				case 3:
					if h.Node.Zone, err = sub.ReadString(); err != nil {
						return nil, err
					}
				case 4:
					if h.Node.Load, err = sub.ReadSint32(); err != nil {
						return nil, err
					}
				default:
					if err := sub.Skip(swt); err != nil {
						return nil, err
					}
				}
			}
		case 3:
			if h.Latencies, err = r.ReadPackedSint32s(); err != nil {
				return nil, err
			}
		case 4:
			if h.Checksums, err = r.ReadPackedFixed64s(); err != nil {
				return nil, err
			}
		case 5:
			tag, err := r.ReadString()
			if err != nil {
				return nil, err
			}
			h.Tags = append(h.Tags, tag)
		default:
			if err := r.Skip(wt); err != nil {
				return nil, err
			}
		}
	}
	return h, nil
}

func main() {
	fmt.Println("=== 1. Protobuf wire-format: nested + packed round trip ===")
	in := &Heartbeat{
		Seq:       9001,
		Node:      NodeInfo{ID: "node-7", Region: "cn-east", Zone: "z2", Load: -12},
		Latencies: []int32{3, -1, 42, 7},
		Checksums: []uint64{0xdeadbeef, 0x0102030405060708},
		Tags:      []string{"hot", "ssd"},
	}
	buf := encodeHeartbeat(in)
	fmt.Printf("encoded %d bytes: %x\n", len(buf), buf)
	out, err := decodeHeartbeat(buf)
	if err != nil {
		panic(err)
	}
	fmt.Printf("decoded: %+v\n", *out)
	if err := validate.Validate(out); err != nil {
		panic(err)
	}
	fmt.Println("reflection validation: PASS")

	fmt.Println("\n=== 2. Truncation safety (out-of-bounds guard) ===")
	cut := len(buf) - 3
	if _, err := decodeHeartbeat(buf[:cut]); err != nil {
		fmt.Printf("truncated to %d/%d bytes -> clean error: %v\n", cut, len(buf), err)
	} else {
		panic("truncated input must not decode")
	}

	fmt.Println("\n=== 3. Hierarchical sharding + locality-aware routing ===")
	rt, err := router.New(4096)
	if err != nil {
		panic(err)
	}
	regions := []string{"cn-east", "cn-north", "us-west"}
	for i := 0; i < 12; i++ {
		region := regions[i%3]
		err := rt.AddNode(router.Node{
			ID:       fmt.Sprintf("node-%02d", i),
			Locality: router.Locality{Region: region, Zone: fmt.Sprintf("%s-z%d", region, i%2)},
		})
		if err != nil {
			panic(err)
		}
	}
	fmt.Println("shard distribution:", rt.Distribution())

	client := router.Locality{Region: "cn-east", Zone: "cn-east-z1"}
	cands, err := rt.Route("user:8848/profile", client, 3)
	if err != nil {
		panic(err)
	}
	fmt.Printf("route key=user:8848/profile from %+v:\n", client)
	for rank, n := range cands {
		fmt.Printf("  rank%d -> %s (%s/%s)\n", rank, n.ID, n.Locality.Region, n.Locality.Zone)
	}

	before := rt.Distribution()
	if err := rt.AddNode(router.Node{ID: "node-12", Locality: router.Locality{Region: "cn-east", Zone: "cn-east-z1"}}); err != nil {
		panic(err)
	}
	after := rt.Distribution()
	moved := 0
	for id, c := range after {
		if id != "node-12" {
			moved += before[id] - c
		}
	}
	fmt.Printf("added node-12: moved %d/%d shards (%.1f%%, ideal %.1f%%)\n",
		moved, rt.NumShards(), 100*float64(moved)/float64(rt.NumShards()), 100.0/13)
}
