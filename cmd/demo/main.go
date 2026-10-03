// Command demo hand-builds a multi-level nested protobuf stream (with packed
// repeated fields), decodes it through the reflection engine, validates the
// result, and exercises the metrics pipeline including a truncated-input
// error path. It prints the decoded struct and a metrics export.
package main

import (
	"fmt"
	"math"
	"os"
	"time"

	"wiremon/metrics"
	"wiremon/wire"
)

// Order mirrors a realistic nested message.
type Order struct {
	OrderID  int64    `protobuf:"zigzag64,1" validate:"min=1"`
	Customer string   `protobuf:"string,2" validate:"required,minlen=1,maxlen=64"`
	Amounts  []int32  `protobuf:"zigzag32,3"` // packed repeated
	Address  Address  `protobuf:"message,4"`
	Tags     []string `protobuf:"string,5"`
	Freight  float64  `protobuf:"fixed64,6"`
	Checksum uint32   `protobuf:"fixed32,7"`
}

type Address struct {
	City string `protobuf:"string,1" validate:"required"`
	Zip  int32  `protobuf:"zigzag32,2" validate:"min=0"`
	Geo  Geo    `protobuf:"message,3"`
}

type Geo struct {
	Lat float32 `protobuf:"fixed32,1"`
	Lng float32 `protobuf:"fixed32,2"`
}

func encodeOrder() []byte {
	geo := wire.NewEncoder()
	geo.AppendTag(1, wire.WireFixed32)
	geo.AppendFixed32(math.Float32bits(31.2304))
	geo.AppendTag(2, wire.WireFixed32)
	geo.AppendFixed32(math.Float32bits(121.4737))

	addr := wire.NewEncoder()
	addr.AppendTag(1, wire.WireBytes)
	addr.AppendString("Shanghai")
	addr.AppendTag(2, wire.WireVarint)
	addr.AppendZigZag32(200000)
	addr.AppendTag(3, wire.WireBytes)
	addr.AppendBytes(geo.Bytes())

	packed := wire.NewEncoder()
	for _, v := range []int32{1999, -50, 3200, 88} {
		packed.AppendZigZag32(v)
	}

	enc := wire.NewEncoder()
	enc.AppendTag(1, wire.WireVarint)
	enc.AppendZigZag64(900001)
	enc.AppendTag(2, wire.WireBytes)
	enc.AppendString("delin")
	enc.AppendTag(3, wire.WireBytes)
	enc.AppendBytes(packed.Bytes())
	enc.AppendTag(4, wire.WireBytes)
	enc.AppendBytes(addr.Bytes())
	for _, t := range []string{"priority", "gift"} {
		enc.AppendTag(5, wire.WireBytes)
		enc.AppendString(t)
	}
	enc.AppendTag(6, wire.WireFixed64)
	enc.AppendFixed64(math.Float64bits(12.75))
	enc.AppendTag(7, wire.WireFixed32)
	enc.AppendFixed32(0x1badb002)
	return enc.Bytes()
}

func main() {
	reg := metrics.NewRegistry()
	decodeLatency := reg.Histogram("decode_latency_seconds", map[string]string{"op": "unmarshal"}, metrics.DefaultLatencyBuckets)
	decoded := reg.Counter("messages_decoded_total", nil)
	cache := reg.HitRate("schema_cache", nil)
	errs := reg.ErrorCodes("decode_errors", nil)
	inflight := reg.Gauge("inflight_decodes", nil)

	stream := encodeOrder()
	fmt.Printf("hand-encoded wire stream: %d bytes\n%x\n\n", len(stream), stream)

	// Decode 1000 times to build a latency distribution. Each iteration uses
	// a fresh target because protobuf merge semantics append repeated fields.
	for i := 0; i < 1000; i++ {
		var o Order
		inflight.Add(1)
		timer := decodeLatency.NewTimer()
		if i%3 == 0 {
			cache.Hit()
		} else {
			cache.Miss()
		}
		if err := wire.Unmarshal(stream, &o); err != nil {
			errs.Record("UNMARSHAL")
			continue
		}
		timer.Observe()
		decoded.Inc()
		inflight.Add(-1)
	}

	var order Order
	if err := wire.Unmarshal(stream, &order); err != nil {
		fmt.Println("unmarshal failed:", err)
		os.Exit(1)
	}
	fmt.Printf("decoded struct: %+v\n", order)
	fmt.Printf("nested address: %+v geo=%+v\n\n", order.Address, order.Address.Geo)

	if err := wire.Validate(&order); err != nil {
		fmt.Println("validation failed:", err)
		os.Exit(1)
	}
	fmt.Println("validation: OK")

	// Error path: truncate the stream mid-field and confirm safe rejection.
	truncated := stream[:len(stream)-3]
	var broken Order
	if err := wire.Unmarshal(truncated, &broken); err != nil {
		if wire.IsTruncated(err) {
			errs.Record("TRUNCATED")
		} else {
			errs.Record("OTHER")
		}
		fmt.Printf("truncated input safely rejected: %v\n\n", err)
	}

	start := time.Now()
	snap := reg.Snapshot()
	fmt.Printf("snapshot took %v\n\n", time.Since(start))

	fmt.Println("=== text export ===")
	snap.ExportText(os.Stdout)
	fmt.Println("=== json export (excerpt) ===")
	snap.ExportJSON(os.Stdout)
}
