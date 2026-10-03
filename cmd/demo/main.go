// Command demo exercises both components end to end:
//  1. hand-encodes a multi-level nested protobuf stream, parses it back,
//     and demonstrates the truncation guard;
//  2. runs a money-transfer saga that fails mid-flight and rolls back via
//     reverse compensation.
package main

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"pbsaga/saga"
	"pbsaga/wire"
)

func main() {
	demoWire()
	fmt.Println(strings.Repeat("-", 60))
	demoSaga()
}

func demoWire() {
	fmt.Println("== Protobuf wire-format round trip ==")

	// Address { 1: string city; 2: fixed32 zip }
	var addr []byte
	addr = wire.AppendTag(addr, 1, wire.WireBytes)
	addr = wire.AppendString(addr, "Shanghai")
	addr = wire.AppendTag(addr, 2, wire.WireFixed32)
	addr = wire.AppendFixed32(addr, 200001)

	// Customer { 1: string name; 2: Address address }
	var cust []byte
	cust = wire.AppendTag(cust, 1, wire.WireBytes)
	cust = wire.AppendString(cust, "alice")
	cust = wire.AppendTag(cust, 2, wire.WireBytes)
	cust = wire.AppendBytes(cust, addr)

	// Order { 1: uint64 id; 2: Customer; 3: sint64 delta; 4: packed tags; 5: packed weights }
	var order []byte
	order = wire.AppendTag(order, 1, wire.WireVarint)
	order = wire.AppendVarint(order, 9876543210)
	order = wire.AppendTag(order, 2, wire.WireBytes)
	order = wire.AppendBytes(order, cust)
	order = wire.AppendTag(order, 3, wire.WireVarint)
	order = wire.AppendZigZag64(order, -4200)
	order = wire.AppendTag(order, 4, wire.WireBytes)
	order = wire.AppendPackedVarints(order, 7, 8, 300, 1<<20)
	order = wire.AppendTag(order, 5, wire.WireBytes)
	order = wire.AppendPackedFixed64(order, 11, 22, 33)

	fmt.Printf("encoded %d bytes: % x\n", len(order), order)

	msg, err := wire.Decode(order)
	if err != nil {
		panic(err)
	}
	id, _ := msg.One(1)
	delta, _ := msg.One(3)
	custField, _ := msg.One(2)
	custMsg, _ := wire.DecodeMessage(custField.Bytes)
	name, _ := custMsg.One(1)
	addrField, _ := custMsg.One(2)
	addrMsg, _ := wire.DecodeMessage(addrField.Bytes)
	city, _ := addrMsg.One(1)
	zip, _ := addrMsg.One(2)
	tagsField, _ := msg.One(4)
	tags, _ := wire.DecodePackedVarints(tagsField.Bytes)
	weightsField, _ := msg.One(5)
	weights, _ := wire.DecodePackedFixed64(weightsField.Bytes)

	fmt.Printf("id=%d delta=%d\n", id.U64, delta.ZigZag64())
	fmt.Printf("customer=%s city=%s zip=%d\n", name.Bytes, city.Bytes, zip.F32)
	fmt.Printf("tags=%v weights=%v\n", tags, weights)

	// Truncation guard: chop the stream mid-field.
	truncated := order[:len(order)-3]
	_, err = wire.Decode(truncated)
	fmt.Printf("truncated stream rejected: %v\n", err)
}

func demoSaga() {
	fmt.Println("== Saga with reverse compensation ==")

	var ledger []string
	step := func(name, action, undo string, fail bool) saga.Step {
		return saga.Step{
			Name: name,
			Action: func(context.Context) error {
				if fail {
					return errors.New("service unavailable")
				}
				ledger = append(ledger, action)
				return nil
			},
			Compensate: func(context.Context) error {
				ledger = append(ledger, undo)
				return nil
			},
		}
	}

	o := saga.New(
		step("reserve-inventory", "inventory reserved", "inventory released", false),
		step("debit-account", "account debited", "account refunded", false),
		step("ship-order", "order shipped", "shipment cancelled", true), // fails
	)

	err := o.Run(context.Background(), "order-9001")
	status, _ := o.Status("order-9001")
	fmt.Printf("run 1: err=%v status=%s\n", err, status)
	fmt.Printf("ledger: %v\n", ledger)

	// Idempotent retry: same transaction ID, nothing re-executes.
	before := len(ledger)
	err2 := o.Run(context.Background(), "order-9001")
	fmt.Printf("run 2 (idempotent retry): err=%v ledger-grew=%v\n", err2, len(ledger) > before)

	fmt.Println("audit log:")
	for _, e := range o.Events() {
		fmt.Printf("  #%d tx=%s step=%q kind=%d err=%v\n", e.Order, e.TxID, e.Step, e.Kind, e.Err)
	}
}
