// Command demo shows: (1) hand-parsing a multi-level nested protobuf stream
// with packed repeated fields, (2) reflection-style schema validation, and
// (3) a long-lived transaction with reverse compensation on failure.
package main

import (
	"context"
	"fmt"
	"sort"

	"protosaga/saga"
	"protosaga/wire"
)

func buildOrder() []byte {
	addr := wire.NewEncoder()
	addr.AddBytesField(1, []byte("Shanghai"))
	addr.AddZigZagField(2, -200000)

	cust := wire.NewEncoder()
	cust.AddBytesField(1, []byte("alice"))
	cust.AddVarintField(2, 1)
	cust.AddBytesField(3, addr.Bytes())

	packed := wire.NewEncoder()
	for _, v := range []uint64{7, 8, 300} {
		packed.AppendVarint(v)
	}

	order := wire.NewEncoder()
	order.AddVarintField(1, 42)
	order.AddBytesField(2, []byte("rush"))
	order.AddBytesField(3, cust.Bytes())
	order.AddBytesField(4, packed.Bytes())
	order.AddZigZagField(5, -5)
	order.AddZigZagField(5, 9)
	return order.Bytes()
}

func orderSchema() *wire.Schema {
	return &wire.Schema{
		Name: "Order",
		Fields: []wire.Field{
			{Name: "id", Number: 1, Kind: wire.KindVarint, Required: true},
			{Name: "note", Number: 2, Kind: wire.KindString},
			{Name: "customer", Number: 3, Kind: wire.KindMessage, Nested: &wire.Schema{
				Name: "Customer",
				Fields: []wire.Field{
					{Name: "name", Number: 1, Kind: wire.KindString, Required: true},
					{Name: "vip", Number: 2, Kind: wire.KindVarint},
					{Name: "address", Number: 3, Kind: wire.KindMessage, Nested: &wire.Schema{
						Name: "Address",
						Fields: []wire.Field{
							{Name: "city", Number: 1, Kind: wire.KindString, Required: true},
							{Name: "zip", Number: 2, Kind: wire.KindSVarint},
						},
					}},
				},
			}},
			{Name: "tags", Number: 4, Kind: wire.KindPackedVarint},
			{Name: "amounts", Number: 5, Kind: wire.KindSVarint, Repeated: true},
		},
	}
}

func printMap(prefix string, m map[string]interface{}) {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		v := m[k]
		if sub, ok := v.(map[string]interface{}); ok {
			fmt.Printf("%s%s:\n", prefix, k)
			printMap(prefix+"  ", sub)
			continue
		}
		fmt.Printf("%s%s = %v\n", prefix, k, v)
	}
}

func main() {
	fmt.Println("=== 1. Protobuf wire-format parse + reflection validation ===")
	raw := buildOrder()
	fmt.Printf("encoded %d bytes: %x\n", len(raw), raw)
	msg, err := wire.Parse(raw)
	if err != nil {
		fmt.Println("parse error:", err)
		return
	}
	out, err := orderSchema().Decode(msg)
	if err != nil {
		fmt.Println("validation error:", err)
		return
	}
	printMap("", out)

	fmt.Println("\n=== 2. Truncated stream -> safe bounds guard ===")
	truncated := raw[:len(raw)-3]
	if _, err := wire.Parse(truncated); err != nil {
		fmt.Println("rejected:", err)
	}

	fmt.Println("\n=== 3. Saga: reverse compensation on failure ===")
	c := saga.NewCoordinator()
	var trail []string
	step := func(name string, fail bool) saga.Step {
		return saga.Step{
			Name: name,
			Action: func(ctx context.Context) error {
				trail = append(trail, "do:"+name)
				if fail {
					return fmt.Errorf("%s failed", name)
				}
				return nil
			},
			Compensate: func(ctx context.Context) error {
				trail = append(trail, "undo:"+name)
				return nil
			},
		}
	}
	steps := []saga.Step{step("reserve-stock", false), step("charge-card", false), step("ship", true)}
	err = c.Execute(context.Background(), "order-42", steps)
	fmt.Println("execute err:", err)
	fmt.Println("trail:", trail)
	fmt.Println("record:", c.Record("order-42"))

	fmt.Println("\n=== 4. Idempotent replay (same tx ID, no re-execution) ===")
	trail = nil
	err = c.Execute(context.Background(), "order-42", steps)
	fmt.Println("replay err:", err)
	fmt.Printf("trail after replay: %v (empty = nothing re-ran)\n", trail)
}
