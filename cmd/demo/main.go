// Demo: schedule messages of mixed priority/size into size-limited
// frames, cancel one in flight, then push the byte stream through the
// parser in small random chunks and reassemble the messages.
package main

import (
	"errors"
	"fmt"
	"math/rand"
	"strings"

	"framedemo/frameq"
)

func main() {
	const maxFrame = 128
	sched, err := frameq.NewScheduler(maxFrame, map[int]int{
		10: 64, // high priority: 64 payload bytes guaranteed per frame
		1:  24, // low priority: 24 payload bytes guaranteed per frame
	})
	if err != nil {
		panic(err)
	}

	msgs := []*frameq.Message{
		{ID: 1, Priority: 10, Data: []byte(strings.Repeat("HIGH-", 120))},  // 600 B, high
		{ID: 2, Priority: 1, Data: []byte("tiny low-priority ping")},       // 22 B, low
		{ID: 3, Priority: 1, Data: []byte(strings.Repeat("low ", 40))},     // 160 B, low
		{ID: 4, Priority: 10, Data: []byte(strings.Repeat("cancel?", 30))}, // 210 B, canceled mid-flight
	}
	for _, m := range msgs {
		if err := sched.Enqueue(m); err != nil {
			panic(err)
		}
	}

	parser := frameq.NewParser(maxFrame)
	reasm := frameq.NewReassembler()
	rng := rand.New(rand.NewSource(42))
	canceled := false

	fmt.Printf("frame limit: %d B (header %d B, fragment header %d B)\n\n",
		maxFrame, frameq.FrameHeaderSize, frameq.FragHeaderSize)
	frameNo := 0
	for {
		frame, ok := sched.NextFrame()
		if !ok {
			break
		}
		frameNo++
		var parts []string
		for _, fr := range frame.Fragments {
			last := ""
			if fr.Last {
				last = " LAST"
			}
			parts = append(parts, fmt.Sprintf("msg=%d off=%d len=%d%s", fr.MsgID, fr.Offset, len(fr.Data), last))
		}
		fmt.Printf("frame %2d (%3d B): %s\n", frameNo, frame.Len(), strings.Join(parts, " | "))

		// Cancel msg 4 after its first bytes have gone out: the sent
		// fragments stay sent, the unsent remainder releases its quota.
		if !canceled && msgs[3].Sent() > 0 {
			fmt.Printf("         -> cancel msg 4 (%d B already sent, %d B released)\n",
				msgs[3].Sent(), msgs[3].Remaining())
			sched.Cancel(4)
			canceled = true
		}

		// Feed the encoded frame to the parser in random small chunks.
		enc := frame.Encode()
		for len(enc) > 0 {
			n := 1 + rng.Intn(17)
			if n > len(enc) {
				n = len(enc)
			}
			parser.Feed(enc[:n])
			enc = enc[n:]
			for {
				f, err := parser.Next()
				if errors.Is(err, frameq.ErrIncomplete) {
					break
				}
				if err != nil {
					panic(fmt.Sprintf("corrupt stream: %v", err))
				}
				for _, fr := range f.Fragments {
					msg, err := reasm.Add(fr)
					if err != nil {
						panic(err)
					}
					if msg != nil {
						preview := string(msg)
						if len(preview) > 40 {
							preview = preview[:40] + "..."
						}
						fmt.Printf("         <- reassembled msg %d (%d B): %q\n", fr.MsgID, len(msg), preview)
					}
				}
			}
		}
	}
	fmt.Printf("\ndone: %d frames, %d B left pending, parser buffer %d B\n",
		frameNo, sched.Pending(), parser.Buffered())
}
