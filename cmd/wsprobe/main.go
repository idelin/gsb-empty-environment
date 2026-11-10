// Command wsprobe is a small demo frontend for the wsprobe parser. It reads a
// captured client-to-server WebSocket byte stream from stdin, feeds it to the
// parser in fixed-size chunks, and prints every frame, every forbidden-string
// hit, and the final stream outcome.
//
// Usage:
//
//	wsprobe -needle secret [-chunk 4096] < capture.bin
package main

import (
	"flag"
	"fmt"
	"io"
	"os"

	"example.org/offlinecli"
)

func main() {
	needle := flag.String("needle", "", "forbidden substring to search for in text messages")
	chunk := flag.Int("chunk", 4096, "feed chunk size in bytes (simulates arbitrary segmentation)")
	flag.Parse()

	stream, err := io.ReadAll(os.Stdin)
	if err != nil {
		fmt.Fprintln(os.Stderr, "read stdin:", err)
		os.Exit(2)
	}

	p := wsprobe.NewParser(*needle, wsprobe.Events{
		OnFrame: func(f wsprobe.Frame) {
			kind := "data"
			if f.Control() {
				kind = "control"
			}
			fmt.Printf("frame %d: %s opcode=0x%x fin=%v len=%d\n", f.Index, kind, f.Opcode, f.Fin, len(f.Payload))
		},
		OnHit: func(h wsprobe.Hit) {
			fmt.Println("HIT:", h)
		},
	})

	for len(stream) > 0 {
		n := min(*chunk, len(stream))
		p.Feed(stream[:n])
		stream = stream[n:]
	}

	r := p.Finish()
	switch r.Outcome {
	case wsprobe.OutcomeOK:
		fmt.Println("outcome: ok (stream ended on a frame boundary)")
	case wsprobe.OutcomeTruncated:
		fmt.Println("outcome: truncated -", r.Detail)
	case wsprobe.OutcomeProtocolError:
		fmt.Println("outcome: protocol error -", r.Err)
		os.Exit(1)
	}
}
