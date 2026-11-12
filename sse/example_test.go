package sse_test

import (
	"bytes"
	"fmt"
	"time"

	"example.org/netsec/q0093/sse"
)

func Example() {
	var buf bytes.Buffer
	// The reconnect interval is a server decision, never caller input.
	w, err := sse.NewWriter(&buf, sse.Config{Retry: 5 * time.Second})
	if err != nil {
		panic(err)
	}

	// Attacker-controlled text carrying newlines and forged field lines is
	// neutralized: every line break splits the data into separate "data:"
	// fields and the forged lines stay payload.
	msg := "hello\r\nworld\nevent: takeover\nid: hijacked"
	if err := w.WriteEvent("push.notify", "evt-1", msg); err != nil {
		panic(err)
	}

	fmt.Print(buf.String())
	// Output:
	// retry: 5000
	// event: push.notify
	// id: evt-1
	// data: hello
	// data: world
	// data: event: takeover
	// data: id: hijacked
	//
}
