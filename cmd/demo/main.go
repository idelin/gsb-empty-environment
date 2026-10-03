// Demo: hand-built protobuf wire stream parsed by the zero-dependency
// codec, plus a multi-tenant DRR fair-scheduling walkthrough.
package main

import (
	"fmt"
	"strings"
	"time"

	"pbsched/scheduler"
	"pbsched/wire"
)

// Order / Item are the reflection-validated schema for the demo message.
type Item struct {
	SKU    string  `wire:"string,1,required"`
	Qty    int64   `wire:"zigzag64,2"`
	Prices []int32 `wire:"zigzag32,3,packed"`
}

type Order struct {
	ID     uint64   `wire:"varint,1,required"`
	Note   string   `wire:"string,2"`
	Item   Item     `wire:"message,3,required"`
	Labels []string `wire:"string,4"`
}

func buildOrder() []byte {
	item := wire.AppendTag(nil, 1, wire.WireBytes)
	item = wire.AppendBytes(item, []byte("WIDGET-7"))
	item = wire.AppendTag(item, 2, wire.WireVarint)
	item = wire.AppendVarint(item, wire.EncodeZigZag64(-3))
	var packed []byte
	for _, p := range []int32{199, -50, 4200} {
		packed = wire.AppendVarint(packed, uint64(wire.EncodeZigZag32(p)))
	}
	item = wire.AppendTag(item, 3, wire.WireBytes)
	item = wire.AppendBytes(item, packed)

	order := wire.AppendTag(nil, 1, wire.WireVarint)
	order = wire.AppendVarint(order, 778899)
	order = wire.AppendTag(order, 2, wire.WireBytes)
	order = wire.AppendBytes(order, []byte("rush delivery"))
	order = wire.AppendTag(order, 3, wire.WireBytes)
	order = wire.AppendBytes(order, item)
	order = wire.AppendTag(order, 4, wire.WireBytes)
	order = wire.AppendBytes(order, []byte("vip"))
	order = wire.AppendTag(order, 4, wire.WireBytes)
	order = wire.AppendBytes(order, []byte("fragile"))
	return order
}

func hexdump(b []byte) string {
	var parts []string
	for i, c := range b {
		if i > 0 && i%16 == 0 {
			parts = append(parts, "\n           ")
		}
		parts = append(parts, fmt.Sprintf("%02x", c))
	}
	return strings.Join(parts, " ")
}

func main() {
	fmt.Println("=== 1. Protobuf wire-format: nested message + packed repeated ===")
	raw := buildOrder()
	fmt.Printf("encoded %d bytes:\n  %s\n", len(raw), hexdump(raw))

	var order Order
	if err := wire.Unmarshal(raw, &order); err != nil {
		fmt.Println("decode failed:", err)
		return
	}
	fmt.Printf("decoded: ID=%d Note=%q Labels=%v\n", order.ID, order.Note, order.Labels)
	fmt.Printf("  nested Item: SKU=%q Qty=%d packed Prices=%v\n",
		order.Item.SKU, order.Item.Qty, order.Item.Prices)

	fmt.Println("\n=== 2. Truncation safety (bounds guard) ===")
	cut := len(raw) - 3
	var broken Order
	err := wire.Unmarshal(raw[:cut], &broken)
	fmt.Printf("truncated stream (%d/%d bytes) -> %v\n", cut, len(raw), err)

	fmt.Println("\n=== 3. Multi-tenant DRR fair scheduling (weights 3:2:1) ===")
	s := scheduler.New(scheduler.Config{Quantum: 1})
	for _, tc := range []struct {
		id     string
		weight int64
	}{
		{"alice", 3}, {"bob", 2}, {"carol", 1},
	} {
		if err := s.AddTenant(tc.id, tc.weight, scheduler.Quota{Limit: 4, Window: time.Minute}); err != nil {
			fmt.Println("AddTenant:", err)
			return
		}
	}
	for i := 0; i < 6; i++ {
		for _, id := range []string{"alice", "bob", "carol"} {
			_ = s.Submit(id, scheduler.Request{Payload: i})
		}
	}
	var seq []string
	for _, r := range s.Drain() {
		seq = append(seq, r.Tenant)
	}
	fmt.Printf("dispatch order: %v\n", seq)
	for _, id := range []string{"alice", "bob", "carol"} {
		st, _ := s.Stats(id)
		fmt.Printf("  %-5s weight=%d dequeued=%d rejected=%d quota-used=%d\n",
			id, st.Weight, st.Dequeued, st.Rejected, st.QuotaUsed)
	}

	fmt.Println("\n=== 4. Quota isolation: carol's exhaustion never blocks alice ===")
	s2 := scheduler.New(scheduler.Config{Quantum: 1})
	_ = s2.AddTenant("alice", 1, scheduler.Quota{Limit: 2, Window: time.Minute})
	_ = s2.AddTenant("carol", 1, scheduler.Quota{Limit: 1, Window: time.Minute})
	_ = s2.Submit("carol", scheduler.Request{})
	err = s2.Submit("carol", scheduler.Request{})
	fmt.Printf("carol 2nd submit -> %v\n", err)
	err = s2.Submit("alice", scheduler.Request{})
	fmt.Printf("alice submit     -> %v (isolated)\n", err)
}
