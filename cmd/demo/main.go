// Demo: schedule messages of different priorities into size-capped frames,
// cancel one mid-flight, then parse the wire bytes back in random-size
// chunks and reassemble the surviving messages.
package main

import (
	"fmt"
	"math/rand"
	"strings"

	"framepack"
)

func main() {
	const maxFrame = framepack.HeaderSize + 16 // 16 payload bytes per frame
	// Three classes: 0 = high, 1 = normal, 2 = low. Per-round payload
	// quanta give high priority preference without starving the rest.
	s, err := framepack.NewScheduler(maxFrame, []int{32, 16, 8}, 4096)
	if err != nil {
		panic(err)
	}

	high, _ := s.Enqueue(0, []byte(strings.Repeat("HIGH-", 20)))     // 100 bytes
	normal, _ := s.Enqueue(1, []byte("normal-priority-message"))     // 23 bytes
	low, _ := s.Enqueue(2, []byte("low"))                            // 3 bytes
	doomed, _ := s.Enqueue(0, []byte(strings.Repeat("CANCEL-", 10))) // 70 bytes

	fmt.Printf("max frame = %d bytes (header %d + payload <= 16)\n", maxFrame, framepack.HeaderSize)
	fmt.Printf("queued: high=%d normal=%d low=%d doomed=%d\n\n", high, normal, low, doomed)

	// Emit frames; cancel `doomed` after its second fragment ships.
	var wire []byte
	sent := map[uint32]int{}
	for i := 0; ; i++ {
		f, ok := s.NextFrame()
		if !ok {
			break
		}
		sent[f.MsgID]++
		fmt.Printf("frame %2d: msg=%d pri=%d start=%-5v end=%-5v payload=%2d wire=%2d\n",
			i, f.MsgID, f.Priority, f.Start(), f.End(), len(f.Payload), framepack.HeaderSize+len(f.Payload))
		wire = append(wire, framepack.EncodeFrame(f)...)
		if f.MsgID == doomed && sent[doomed] == 2 {
			released, _ := s.Cancel(doomed)
			fmt.Printf("-- cancel msg=%d: %d unsent bytes released, %d fragments already on the wire\n",
				doomed, released, sent[doomed])
		}
	}

	// Parse the stream back in random-size chunks.
	p := framepack.NewParser(maxFrame, 1<<20)
	r := framepack.NewReassembler(1 << 20)
	rng := rand.New(rand.NewSource(42))
	fmt.Println("\nreassembled messages:")
	for len(wire) > 0 {
		n := 1 + rng.Intn(37) // arbitrary chunk sizes, may split frames
		if n > len(wire) {
			n = len(wire)
		}
		frames, err := p.Feed(wire[:n])
		if err != nil {
			panic(err)
		}
		wire = wire[n:]
		for _, f := range frames {
			payload, done, err := r.Push(f)
			if err != nil {
				panic(err)
			}
			if done {
				fmt.Printf("msg=%d pri=%d len=%d %q\n", f.MsgID, f.Priority, len(payload), preview(payload))
			}
		}
	}
	fmt.Println("\nnote: msg", doomed, "never completes — canceled; its 2 sent fragments remain on the wire")
}

func preview(b []byte) string {
	if len(b) <= 32 {
		return string(b)
	}
	return string(b[:29]) + "..."
}
