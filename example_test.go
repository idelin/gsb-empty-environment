package netsec_test

import (
	"fmt"

	netsec "example.org/netsec/q0312"
)

func ExampleMessage() {
	from, err := netsec.NewAddress("发货人", "sender@example.com")
	if err != nil {
		panic(err)
	}

	msg := netsec.NewMessage()
	if err := msg.SetFrom(from); err != nil {
		panic(err)
	}
	if err := msg.SetTo(
		netsec.Address{Name: "Alice", Address: "alice@example.com"},
		"bob@example.com",
	); err != nil {
		panic(err)
	}
	if err := msg.SetSubject("月度报告"); err != nil {
		panic(err)
	}
	if err := msg.SetTextBody("报告见附件。\n"); err != nil {
		panic(err)
	}
	if err := msg.AddAttachment("报告 2026.pdf", "application/pdf", []byte("%PDF")); err != nil {
		panic(err)
	}

	wire, err := msg.Build()
	if err != nil {
		panic(err)
	}

	envelope, err := netsec.NewEnvelope("sender@example.com", []string{
		"alice@example.com",
		"bob@example.com",
	})
	if err != nil {
		panic(err)
	}
	recipients, err := envelope.RcptTo()
	if err != nil {
		panic(err)
	}

	fmt.Printf("%d bytes for %d envelope recipients\n", len(wire), len(recipients))
}
