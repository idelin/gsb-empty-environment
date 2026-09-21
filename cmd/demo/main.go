// Command demo simulates a sender streaming length-prefixed packets that
// carry 8-bit sequence numbers across the wrap point, with a gap, a
// duplicate, and a too-old packet. The byte stream is chopped into random
// small chunks before parsing.
package main

import (
	"errors"
	"fmt"
	"math/rand"

	"seqwin"
)

func main() {
	space := seqwin.NewSpace(8)
	recv, err := seqwin.NewReceiver(space, 254, 16)
	if err != nil {
		panic(err)
	}

	// Sender: 254..2 across the wrap, with 0 dropped (gap), 2 sent twice
	// (in-window duplicate), 249 arriving late (too old), then 0
	// retransmitted to close the gap.
	send := []uint32{254, 255, 1, 2, 2, 249, 0}
	var stream []byte
	for _, s := range send {
		f, _ := seqwin.EncodeFrame([]byte{byte(s)})
		stream = append(stream, f...)
	}

	// Chop the stream into random 1..5 byte chunks, like a real network.
	parser := seqwin.NewFrameParser(64)
	rng := rand.New(rand.NewSource(42))
	var chunks [][]byte
	for rest := stream; len(rest) > 0; {
		n := 1 + rng.Intn(5)
		if n > len(rest) {
			n = len(rest)
		}
		chunks = append(chunks, rest[:n])
		rest = rest[n:]
	}
	fmt.Printf("stream: %d bytes in %d chunks (sizes 1..5)\n\n", len(stream), len(chunks))

	for i, c := range chunks {
		parser.Write(c)
		for {
			payload, err := parser.Next()
			if errors.Is(err, seqwin.ErrNeedMore) {
				break
			}
			if err != nil {
				fmt.Printf("chunk %2d: bad frame: %v\n", i, err)
				continue
			}
			seq := uint32(payload[0])
			accepted, err := recv.Receive(seq)
			switch {
			case err != nil:
				fmt.Printf("chunk %2d: seq %3d rejected: %v\n", i, seq, err)
			case !accepted:
				fmt.Printf("chunk %2d: seq %3d duplicate (count unchanged)\n", i, seq)
			default:
				fmt.Printf("chunk %2d: seq %3d accepted\n", i, seq)
			}
			fmt.Printf("          ack ranges: %v  (next=%d, count=%d)\n",
				recv.AckRanges(), recv.Next(), recv.Count())
		}
	}

	// Half-space rule: distance 128 in 8-bit space is ambiguous.
	if _, err := space.Less(0, 128); err != nil {
		fmt.Printf("\nLess(0, 128) correctly refused: %v\n", err)
	}
}
