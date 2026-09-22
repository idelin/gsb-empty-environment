// Command demo shows two framemux connections multiplexing streams over a
// single byte channel, acknowledging frames, and switching escape epochs.
package main

import (
	"fmt"

	"framemux"
)

func pump(from, to *framemux.Conn) {
	wire := from.TakeOutput()
	if len(wire) == 0 {
		return
	}
	if _, err := to.Write(wire); err != nil {
		fmt.Printf("write error (resynced): %v\n", err)
	}
}

func drain(name string, c *framemux.Conn) {
	for {
		f, ok := c.Recv()
		if !ok {
			return
		}
		switch f.Type {
		case framemux.TypeData:
			fmt.Printf("%s: stream=%d seq=%d epoch=%d payload=%q\n",
				name, f.Stream, f.Seq, f.Epoch, f.Payload)
		case framemux.TypeAck:
			fmt.Printf("%s: ack stream=%d up-to-seq=%d\n", name, f.Stream, f.Seq)
		}
	}
}

func main() {
	cfg := framemux.Config{MaxPayload: 256, MaxStreamBuffer: 1024, MaxTotalBuffer: 4096}
	a, b := framemux.NewConn(cfg), framemux.NewConn(cfg)

	// Two logical streams share one byte channel; the payload of stream 2
	// contains every byte value, including delimiter and escape bytes.
	a.Send(1, []byte("hello stream 1"))
	all := make([]byte, 256)
	for i := range all {
		all[i] = byte(i)
	}
	a.Send(2, all)
	pump(a, b)
	drain("B", b)

	// B acknowledges stream 1; A learns it may retire seq 0.
	b.SendAck(1, 0)
	pump(b, a)
	drain("A", a)

	// Switch escape rules to epoch 1: propose -> confirm -> switch at frame
	// boundary. Traffic before and after the switch keeps flowing.
	a.ProposeEpoch(1)
	pump(a, b) // B confirms and switches
	pump(b, a) // A receives the confirm and switches
	a.Send(1, []byte("post-switch frame"))
	pump(a, b)
	drain("B", b)

	fmt.Printf("B stats: %+v\n", b.Stats())
}
