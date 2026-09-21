// Command demo runs two mux sessions back to back over an in-memory byte
// channel, delivering bytes in random chunks, and exercises: multi-stream
// transfer, per-stream windows, control priority under quota, half-close,
// duplicate window updates, and late data after close.
package main

import (
	"bytes"
	"fmt"
	"math/rand"

	"muxdemo"
)

// deliver flushes one session and feeds the bytes to the peer in random
// small chunks, like a real network would segment them.
func deliver(from, to *mux.Session, rng *rand.Rand, label string) error {
	wire := from.Flush()
	if len(wire) > 0 {
		fmt.Printf("[wire] %s: %d bytes\n", label, len(wire))
	}
	for len(wire) > 0 {
		n := 1 + rng.Intn(len(wire))
		if err := to.Receive(wire[:n]); err != nil {
			return fmt.Errorf("%s: %w", label, err)
		}
		wire = wire[n:]
	}
	return nil
}

func main() {
	rng := rand.New(rand.NewSource(7))
	a := mux.NewSession(1024, 1<<20, 4096)
	b := mux.NewSession(1024, 1<<20, 4096)

	received := map[uint32][]byte{}
	b.OnData = func(id uint32, d []byte) { received[id] = append(received[id], d...) }
	b.OnClose = func(id uint32) { fmt.Printf("[b] stream %d: peer half-closed\n", id) }

	fmt.Println("== open streams 1 and 2, send data on both ==")
	must(a.OpenStream(1))
	must(a.OpenStream(2))
	msg1 := bytes.Repeat([]byte("stream-one-"), 1000) // 11000 bytes > window
	msg2 := []byte("stream-two says hi")
	must(a.SendData(1, msg1))
	must(a.SendData(2, msg2))
	must(deliver(a, b, rng, "a -> b"))
	fmt.Printf("[b] stream 1 got %d/%d bytes (window-limited), stream 2 got %q\n",
		len(received[1]), len(msg1), received[2])
	fmt.Println("    -> stream 1's exhausted window did not block stream 2")

	fmt.Println("\n== window updates flow back, stream 1 resumes ==")
	must(deliver(b, a, rng, "b -> a (WINDOW_UPDATEs)"))
	must(deliver(a, b, rng, "a -> b"))
	for len(received[1]) < len(msg1) {
		must(deliver(b, a, rng, "b -> a"))
		must(deliver(a, b, rng, "a -> b"))
	}
	fmt.Printf("[b] stream 1 complete: %d bytes, content ok=%v\n",
		len(received[1]), bytes.Equal(received[1], msg1))

	fmt.Println("\n== duplicate window update is not double-counted ==")
	must(a.OpenStream(3))
	must(deliver(a, b, rng, "a -> b"))
	must(b.SendData(3, []byte("0123456789")))
	must(deliver(b, a, rng, "b -> a"))
	wu := a.Flush() // a's WINDOW_UPDATE for the 10 bytes
	must(b.Receive(wu))
	w1 := sendWindow(b, 3)
	must(b.Receive(wu)) // same bytes again, e.g. duplicated segment
	w2 := sendWindow(b, 3)
	fmt.Printf("[b] send window after first update: %d, after duplicate: %d (unchanged=%v)\n",
		w1, w2, w1 == w2)

	fmt.Println("\n== half-close: a finishes sending, b can still answer ==")
	var aGot []byte
	a.OnData = func(id uint32, d []byte) { aGot = append(aGot, d...) }
	must(a.OpenStream(4))
	must(a.SendData(4, []byte("request")))
	must(a.Close(4))
	must(deliver(a, b, rng, "a -> b"))
	must(b.SendData(4, []byte("response")))
	must(deliver(b, a, rng, "b -> a"))
	fmt.Printf("[a] got response on half-closed stream: %q\n", aGot)

	fmt.Println("\n== late data after full close is a protocol error ==")
	must(b.Close(4))
	must(deliver(b, a, rng, "b -> a (CLOSE)"))
	fmt.Println("[a] stream 4 now fully closed")
	late, _ := mux.EncodeFrame(mux.Frame{Type: mux.FrameData, StreamID: 4, Payload: []byte("late")}, 1024)
	err := a.Receive(late)
	fmt.Printf("[a] receiving post-close data: %v\n", err)
}

func sendWindow(s *mux.Session, id uint32) int64 {
	w, ok := s.SendWindow(id)
	if !ok {
		panic("stream not found")
	}
	return w
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}
