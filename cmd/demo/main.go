// Demo: handshake, chunked data delivery, replay dedup, unknown message,
// re-entrant handshake, premature business frame, and timeout.
package main

import (
	"fmt"

	proto "example.com/fsmproto"
)

func mustFrame(typ proto.MsgType, seq uint32, payload []byte) []byte {
	b, err := proto.Encode(typ, seq, payload)
	if err != nil {
		panic(err)
	}
	return b
}

func show(m *proto.Machine, label string) {
	fmt.Printf("%-34s state=%s\n", label, m.State())
	for _, out := range m.DrainOutputs() {
		var p proto.Parser
		p.Feed(out)
		f, _ := p.Next()
		fmt.Printf("  -> output: %s seq=%d payload=%q\n", f.Type, f.Seq, f.Payload)
	}
}

func main() {
	m, err := proto.Compile(proto.DefaultTable())
	if err != nil {
		panic(err)
	}

	fmt.Println("== handshake ==")
	if err := m.Feed(mustFrame(proto.MsgHello, 0, nil)); err != nil {
		fmt.Println("  error:", err)
	}
	show(m, "recv Hello")
	if err := m.Feed(mustFrame(proto.MsgHelloAck, 0, nil)); err != nil {
		fmt.Println("  error:", err)
	}
	show(m, "recv HelloAck")

	fmt.Println("\n== data frame fed byte-by-byte (arbitrary chunking) ==")
	wire := mustFrame(proto.MsgData, 1, []byte("hello"))
	for i, b := range wire {
		if err := m.Feed([]byte{b}); err != nil {
			fmt.Println("  error:", err)
		}
		if i == len(wire)/2 {
			fmt.Printf("  fed %d/%d bytes, state=%s (partial frame waits)\n", i+1, len(wire), m.State())
		}
	}
	show(m, "recv Data seq=1 (1-byte chunks)")

	fmt.Println("\n== replay of the same input (dedup rule) ==")
	if err := m.Feed(wire); err != nil { // identical bytes, seq=1 again
		fmt.Println("  error:", err)
	}
	show(m, "replay Data seq=1")
	fmt.Printf("  duplicates=%d (payload dropped, ack re-emitted)\n", m.Duplicates)

	fmt.Println("\n== unknown message type (independent event) ==")
	if err := m.Feed(mustFrame(proto.MsgType(0x7f), 0, nil)); err != nil {
		fmt.Println("  error:", err)
	}
	show(m, "recv type 0x7f")
	fmt.Printf("  unknown=%d\n", m.Unknown)

	fmt.Println("\n== re-entrant handshake (independent event) ==")
	if err := m.Feed(mustFrame(proto.MsgHello, 0, nil)); err != nil {
		fmt.Println("  error:", err)
	}
	show(m, "recv Hello again")
	fmt.Printf("  reentries=%d\n", m.Reentries)

	fmt.Println("\n== timeout (independent event) ==")
	if err := m.OnTimeout(); err != nil {
		fmt.Println("  error:", err)
	}
	show(m, "idle timeout")

	fmt.Println("\n== business frame before handshake ==")
	m2, _ := proto.Compile(proto.DefaultTable())
	err = m2.Feed(mustFrame(proto.MsgData, 1, []byte("too-early")))
	fmt.Printf("  recv Data in Init -> error: %v, state=%s\n", err, m2.State())

	fmt.Println("\n== bad data vs missing data ==")
	m3, _ := proto.Compile(proto.DefaultTable())
	err = m3.Feed([]byte{0xff, 0xff, 0x01}) // declared length 65535 > quota
	fmt.Printf("  oversized length -> %v\n", err)
	m4, _ := proto.Compile(proto.DefaultTable())
	err = m4.Feed([]byte{0x00, 0x05, 0x01}) // legal header, body missing
	fmt.Printf("  truncated frame  -> %v (waits for more bytes), state=%s\n", err, m4.State())
}
