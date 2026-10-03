// Command demo is an end-to-end walkthrough of the pbufsim toolkit:
//
//  1. hand-encode a nested protobuf stream (packed repeated included)
//  2. parse it with the zero-dependency wire parser
//  3. validate & decode it with the reflection schema engine
//  4. show safe-bounds reporting on a truncated stream
//  5. run a deterministic 1,000,000-step simulation
//  6. inject a fault, observe divergence, replay to reproduce the bug
package main

import (
	"fmt"
	"time"

	"pbufsim/message"
	"pbufsim/schema"
	"pbufsim/sim"
	"pbufsim/wire"
)

func main() {
	fmt.Println("=== 1. Hand-encode a nested protobuf stream ===")
	buf := encodeTick()
	fmt.Printf("encoded %d bytes: %x...\n", len(buf), buf[:16])

	fmt.Println("\n=== 2. Generic parse (schema-free) ===")
	gm, err := message.Parse(buf)
	must(err)
	for _, f := range gm.Fields {
		fmt.Printf("  field %d type=%s", f.Num, f.Type)
		switch f.Type {
		case wire.Varint:
			fmt.Printf(" varint=%d", f.Varint)
		case wire.Bytes:
			fmt.Printf(" len=%d", len(f.Raw))
		}
		fmt.Println()
	}

	fmt.Println("\n=== 3. Reflection validation engine ===")
	rec, violations, err := (&schema.Engine{}).Decode(tickSchema(), buf)
	must(err)
	if len(violations) != 0 {
		fmt.Println("  violations:", violations)
		return
	}
	ts, _ := rec.Get("ts")
	vols, _ := rec.Get("volumes")
	deltas, _ := rec.Get("deltas")
	orders, _ := rec.Get("orders")
	fmt.Printf("  ts=%v volumes=%v deltas=%v\n", ts, vols, deltas)
	for i, o := range orders.([]*schema.Record) {
		id, _ := o.Get("id")
		price, _ := o.Get("price")
		sym, _ := o.Get("symbol")
		fmt.Printf("  order[%d] id=%v price=%v symbol=%v\n", i, id, price, sym)
	}

	fmt.Println("\n=== 4. Truncated stream -> safe bounds error ===")
	cut := buf[:len(buf)-3]
	_, _, err = (&schema.Engine{}).Decode(tickSchema(), cut)
	fmt.Printf("  decode(%d of %d bytes) -> %v\n", len(cut), len(buf), err)

	fmt.Println("\n=== 5. Deterministic simulation: 1,000,000 steps ===")
	start := time.Now()
	base := sim.Replay(2024, nil, workload, 1_000_000)
	fmt.Printf("  %d steps in %v, final hash = %016x\n",
		len(base.Steps), time.Since(start).Round(time.Millisecond), base.FinalHash())
	again := sim.Replay(2024, nil, workload, 1_000_000)
	fmt.Printf("  rerun identical: %v\n", sim.Diff(base, again) == "")

	fmt.Println("\n=== 6. Fault injection & bug reproduction ===")
	faults := []sim.FaultFunc{sim.DropEvery("tick", 4093)}
	faulty := sim.Replay(2024, faults, workload, 1_000_000)
	fmt.Printf("  baseline vs faulty: %s\n", sim.Diff(base, faulty))
	repro := sim.Replay(2024, faults, workload, 1_000_000)
	fmt.Printf("  faulty hash = %016x, replay reproduces exactly: %v\n",
		faulty.FinalHash(), sim.Diff(faulty, repro) == "")
}

// workload is a deterministic event chain: each tick consumes one RNG
// draw, observes a running price, and schedules the next tick.
func workload(e *sim.Engine) {
	var price uint64 = 100_000
	e.Handle("tick", func(e *sim.Engine, ev sim.Event) {
		delta := e.Uint64n(201) // 0..200
		price = price + delta - 100
		e.Observe(price)
		e.Schedule(1, "tick", nil)
	})
	e.Schedule(0, "tick", nil)
}

func tickSchema() *schema.Message {
	order := schema.NewMessage("Order",
		schema.Field{Num: 1, Name: "id", Kind: schema.KindUint64, Required: true},
		schema.Field{Num: 2, Name: "price", Kind: schema.KindSint64, Min: schema.Int64(0)},
		schema.Field{Num: 3, Name: "symbol", Kind: schema.KindString, Required: true, MaxLen: 8},
	)
	return schema.NewMessage("Tick",
		schema.Field{Num: 1, Name: "ts", Kind: schema.KindUint64, Required: true},
		schema.Field{Num: 2, Name: "orders", Kind: schema.KindMessage, Repeated: true, Msg: order},
		schema.Field{Num: 3, Name: "volumes", Kind: schema.KindUint32, Repeated: true, Packed: true},
		schema.Field{Num: 4, Name: "deltas", Kind: schema.KindSint32, Repeated: true, Packed: true},
	)
}

func encodeTick() []byte {
	var w wire.Writer
	w.AppendTag(1, wire.Varint)
	w.AppendVarint(1_700_000_000)
	encodeOrder(&w, 7, 9950, "BTCUSD")
	encodeOrder(&w, 8, 12300, "ETHUSD")
	var packed wire.Writer
	for _, v := range []uint64{100, 250, 175} {
		packed.AppendVarint(v)
	}
	w.AppendTag(3, wire.Bytes)
	w.AppendBytes(packed.Bytes())
	var deltas wire.Writer
	for _, d := range []int64{-12, 3, 9} {
		deltas.AppendZigZag(d)
	}
	w.AppendTag(4, wire.Bytes)
	w.AppendBytes(deltas.Bytes())
	return w.Bytes()
}

func encodeOrder(w *wire.Writer, id uint64, price int64, symbol string) {
	var o wire.Writer
	o.AppendTag(1, wire.Varint)
	o.AppendVarint(id)
	o.AppendTag(2, wire.Varint)
	o.AppendZigZag(price)
	o.AppendTag(3, wire.Bytes)
	o.AppendBytes([]byte(symbol))
	w.AppendTag(2, wire.Bytes)
	w.AppendBytes(o.Bytes())
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}
