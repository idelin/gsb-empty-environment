// Command example demonstrates the framemux protocol: several logical
// streams over one byte channel, arbitrary input chunking, a negotiated
// encoding-epoch switch, and loss recovery via retransmission.
package main

import (
	"bytes"
	"fmt"
	"log"

	"framemux"
)

// endpoint couples a Mux with the outbound buffer facing its peer.
type endpoint struct {
	m    *framemux.Mux
	peer *endpoint
	out  [][]byte // one entry per Write, i.e. one whole frame
	drop int      // drop every n-th flushed frame (0 = reliable)
	sent int
}

func (e *endpoint) Write(p []byte) (int, error) {
	e.out = append(e.out, append([]byte(nil), p...))
	return len(p), nil
}

// flush ships every queued frame to the peer, chopped into odd-sized
// pieces to prove the parser handles arbitrary chunking. Every n-th
// frame is lost entirely when e.drop > 0.
func (e *endpoint) flush() {
	out := e.out
	e.out = nil
	for _, frame := range out {
		e.sent++
		if e.drop > 0 && e.sent%e.drop == 0 {
			fmt.Printf("  [wire] dropped frame of %d bytes\n", len(frame))
			continue
		}
		for len(frame) > 0 {
			n := 5 // deliberately awkward chunk size
			if n > len(frame) {
				n = len(frame)
			}
			if err := e.peer.m.Feed(frame[:n]); err != nil {
				log.Printf("feed: %v", err)
			}
			frame = frame[n:]
		}
	}
}

func recvAll(m *framemux.Mux, stream uint8) []byte {
	var out []byte
	for {
		p, ok := m.Receive(stream)
		if !ok {
			return out
		}
		out = append(out, p...)
	}
}

func main() {
	a := &endpoint{}
	b := &endpoint{}
	a.peer, b.peer = b, a
	a.m = framemux.NewMux(a, framemux.Config{})
	b.m = framemux.NewMux(b, framemux.Config{})

	fmt.Println("== 1. multiplexed streams over one channel ==")
	a.m.Send(1, []byte("hello from stream 1"))
	a.m.Send(2, []byte("stream 2 says hi"))
	a.m.Send(1, []byte("second message on stream 1"))
	a.flush()
	fmt.Printf("stream 1: %q\n", recvAll(b.m, 1))
	fmt.Printf("stream 2: %q\n", recvAll(b.m, 2))
	b.flush() // ACKs back

	fmt.Println("== 2. negotiated encoding-epoch switch ==")
	if err := a.m.ProposeEpoch(1); err != nil {
		log.Fatal(err)
	}
	a.flush() // proposal crosses; b confirms and switches
	b.flush() // confirmation crosses; a switches
	fmt.Printf("epochs after negotiation: a=%d b=%d\n", a.m.Epoch(), b.m.Epoch())

	fmt.Println("== 3. payload containing all 256 byte values ==")
	var all [256]byte
	for i := range all {
		all[i] = byte(i)
	}
	a.m.Send(3, all[:])
	a.flush()
	got := recvAll(b.m, 3)
	fmt.Printf("roundtrip of %d payload bytes: %v\n", len(got), bytes.Equal(got, all[:]))
	b.flush()

	fmt.Println("== 4. lossy wire, retransmit, no duplicates ==")
	a.drop = 4 // lose every 4th frame a->b
	want := []byte("the quick brown fox jumps over the lazy dog")
	a.m.Send(5, want)
	var recovered []byte
	for i := 0; i < 20 && a.m.Unacked() > 0; i++ {
		a.flush()
		b.flush()
		recovered = append(recovered, recvAll(b.m, 5)...)
		a.m.Retransmit()
	}
	a.flush()
	b.flush()
	recovered = append(recovered, recvAll(b.m, 5)...)
	fmt.Printf("recovered stream 5 intact: %v (unacked=%d)\n",
		bytes.Equal(recovered, want), a.m.Unacked())

	for _, ev := range b.m.Events() {
		fmt.Printf("  [event] kind=%d stream=%d epoch=%d err=%v\n", ev.Kind, ev.Stream, ev.Epoch, ev.Err)
	}
}
