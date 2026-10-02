// Command demo hand-builds a multi-level nested protobuf wire stream,
// parses it with the zero-dependency codec, restores it into structs via
// the reflection validation engine, and shows the adaptive load-aware
// threshold controller reacting to changing external pressure.
package main

import (
	"context"
	"fmt"
	"math/rand"
	"time"

	"protowire/adaptive"
	"protowire/validate"
	"protowire/wire"
)

type Address struct {
	City string `pb:"1,string" validate:"required"`
	Zip  int64  `pb:"2,zigzag" validate:"min=0,max=99999"`
}

type Person struct {
	Name    string   `pb:"1,string" validate:"required"`
	ID      uint64   `pb:"2,varint" validate:"min=1"`
	Balance int64    `pb:"3,zigzag"`
	Score   uint64   `pb:"4,fixed64"`
	Level   uint32   `pb:"5,fixed32"`
	Lucky   []int64  `pb:"6,packed"`
	Addr    *Address `pb:"7,message"`
	Avatar  []byte   `pb:"8,bytes"`
}

func buildPersonStream() []byte {
	var addr []byte
	addr = wire.AppendTag(addr, 1, wire.WireLengthDelimited)
	addr = wire.AppendBytes(addr, []byte("Shanghai"))
	addr = wire.AppendTag(addr, 2, wire.WireVarint)
	addr = wire.AppendVarint(addr, wire.EncodeZigZag(20000))

	var packed []byte
	for _, v := range []int64{7, -3, 42, 1000000} {
		packed = wire.AppendVarint(packed, wire.EncodeZigZag(v))
	}

	var msg []byte
	msg = wire.AppendTag(msg, 1, wire.WireLengthDelimited)
	msg = wire.AppendBytes(msg, []byte("Ada"))
	msg = wire.AppendTag(msg, 2, wire.WireVarint)
	msg = wire.AppendVarint(msg, 9001)
	msg = wire.AppendTag(msg, 3, wire.WireVarint)
	msg = wire.AppendVarint(msg, wire.EncodeZigZag(-123456))
	msg = wire.AppendTag(msg, 4, wire.WireFixed64)
	msg = wire.AppendFixed64(msg, 0xDEADBEEFCAFEBABE)
	msg = wire.AppendTag(msg, 5, wire.WireFixed32)
	msg = wire.AppendFixed32(msg, 99)
	msg = wire.AppendTag(msg, 6, wire.WireLengthDelimited)
	msg = wire.AppendBytes(msg, packed)
	msg = wire.AppendTag(msg, 7, wire.WireLengthDelimited)
	msg = wire.AppendBytes(msg, addr)
	msg = wire.AppendTag(msg, 8, wire.WireLengthDelimited)
	msg = wire.AppendBytes(msg, []byte{0x89, 0x50, 0x4E, 0x47})
	return msg
}

func main() {
	stream := buildPersonStream()
	fmt.Printf("wire stream: %d bytes\n%x\n\n", len(stream), stream)

	fields, err := wire.ParseMessage(stream)
	if err != nil {
		panic(err)
	}
	fmt.Printf("top-level fields decoded: %d\n", len(fields))
	for _, f := range fields {
		fmt.Printf("  field=%d wiretype=%d\n", f.Number, f.Type)
	}

	var p Person
	if err := validate.Unmarshal(stream, &p); err != nil {
		panic(err)
	}
	fmt.Printf("\nrestored struct: %+v\n  addr: %+v\n", p, *p.Addr)

	truncated := stream[:len(stream)-2]
	if _, err := wire.ParseMessage(truncated); err != nil {
		fmt.Printf("\ntruncated stream safely rejected: %v\n", err)
	}

	fmt.Println("\n--- adaptive load-aware controller ---")
	ctl := adaptive.NewController(adaptive.Options{
		Min: 2, Max: 128, Initial: 16,
		HighWater: 0.75, LowWater: 0.35,
		Beta: 0.5, Step: 4, Alpha: 0.4,
		Cooldown: 50 * time.Millisecond,
	})
	defer ctl.Close()

	rng := rand.New(rand.NewSource(1))
	pressure := func() float64 {
		switch {
		case time.Now().UnixMilli()%900 < 300:
			return 0.9 + 0.1*rng.Float64() // spike
		case time.Now().UnixMilli()%900 < 600:
			return 0.5 // nominal
		default:
			return 0.1 // idle
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 1200*time.Millisecond)
	defer cancel()
	ctl.Start(ctx, 60*time.Millisecond, func() adaptive.Metrics {
		return adaptive.Metrics{Load: pressure()}
	})

	tick := time.NewTicker(150 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			fmt.Printf("final: limit=%d smoothed-load=%.2f\n", ctl.Limit(), ctl.Load())
			return
		case <-tick.C:
			fmt.Printf("t=%s limit=%3d smoothed-load=%.2f inflight=%d\n",
				time.Now().Format("15:04:05.000"), ctl.Limit(), ctl.Load(), ctl.Inflight())
		}
	}
}
