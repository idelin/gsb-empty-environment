// Command demo shows the chained framing parser end to end.
package main

import (
	"errors"
	"fmt"
	"os"

	chain "chainframe"
)

func main() {
	// 1) Build a valid stream: frame0, frame1, frame2.
	b := chain.NewBuilder()
	f0 := b.Add([]byte("hello"))
	f1 := b.Add([]byte("chained"))
	f2 := b.Add([]byte("world"))
	stream := b.Bytes()
	fmt.Printf("stream: %d bytes across 3 frames (min frame size %d)\n\n", len(stream), chain.MinFrameLen)

	// 2) Feed in arbitrary byte-sized chunks (here 7 bytes per Append).
	p := chain.NewParser()
	chunk := 7
	for i := 0; i < len(stream); i += chunk {
		end := i + chunk
		if end > len(stream) {
			end = len(stream)
		}
		p.Append(stream[i:end])
		for {
			ev, err := p.Next()
			if errors.Is(err, chain.ErrNeedMoreData) {
				break
			}
			if err != nil {
				fail("parse", err)
			}
			fmt.Printf("accept seq=%d payload=%q offset=%d\n", ev.Frame.Seq, ev.Frame.Payload, ev.Offset)
		}
	}
	cp := p.Checkpoint()
	fmt.Printf("checkpoint after stream: seq=%d digest=%x…\n\n", cp.Seq, cp.LastDigest[:8])

	// 3) Verify a suffix against the checkpoint (restart / resume case).
	cont, err := chain.NewBuilderAt(cp)
	must(err)
	cont.Add([]byte("after-restart"))
	sp, err := chain.NewParserAt(cp)
	must(err)
	sp.Append(cont.Bytes())
	ev, err := sp.Next()
	must(err)
	fmt.Printf("suffix accepted from checkpoint: seq=%d payload=%q\n\n", ev.Frame.Seq, ev.Frame.Payload)

	// 4) An exact duplicate frame is reported but never accepted as new.
	dp := chain.NewParser()
	dp.Append(append(f0.Encode(), f1.Encode()...))
	dp.Next()
	dp.Next()
	dp.Append(f1.Encode())
	dev, derr := dp.Next()
	must(derr)
	fmt.Printf("resent frame -> kind=%v seq=%d (checkpoint still seq=%d)\n\n",
		kindName(dev.Kind), dev.Frame.Seq, dp.Checkpoint().Seq)

	// 5) Swap two adjacent, individually CRC-valid frames: chain breaks.
	swapped := chain.NewParser()
	swapped.Append(append(f1.Encode(), f0.Encode()...))
	_, err = swapped.Next()
	report("swapped adjacent frames (first frame moved to 2nd)", err)

	// 5b) f2 delivered first: seq jumps AND its prevDigest points at the
	// unseen f1, so even a correct CRC cannot hide the broken link.
	link := chain.NewParser()
	link.Append(f2.Encode())
	_, err = link.Next()
	report("frame with correct CRC but unseen predecessor", err)

	// 6) Same sequence number as an accepted frame but different bytes:
	// its own CRC is valid and the link matches, but it conflicts with the
	// frame already stored at that sequence number.
	evil := chain.Frame{Seq: 1, PrevDigest: f0.Digest(), Payload: []byte("TAMPER")}
	evilP := chain.NewParser()
	evilP.Append(append(f0.Encode(), f1.Encode()...))
	evilP.Next()
	evilP.Next() // original seq=1 accepted
	evilP.Append(evil.Encode())
	_, err = evilP.Next()
	report("same seq, different content", err)

	// 7) Missing middle frame: gap is a hard error.
	gap := chain.NewParser()
	gap.Append(append(f0.Encode(), f2.Encode()...))
	gap.Next()
	_, err = gap.Next()
	report("middle frame missing", err)

	// 8) Wrong checkpoint: a well-formed but incorrect digest is rejected.
	bogus := chain.Checkpoint{Seq: cp.Seq, LastDigest: make([]byte, 32)}
	for i := range bogus.LastDigest {
		bogus.LastDigest[i] = 0xFF
	}
	wp, err := chain.NewParserAt(bogus)
	must(err)
	wp.Append(cont.Bytes())
	_, err = wp.Next()
	report("suffix against wrong checkpoint", err)

	// 9) A truncated LAST frame is "need more data", not corruption; it
	// completes once the remaining bytes arrive.
	tp := chain.NewParser()
	tp.Append(stream[:len(stream)-2])
	for {
		_, err := tp.Next()
		if errors.Is(err, chain.ErrNeedMoreData) {
			break
		}
		must(err)
	}
	fmt.Println("truncated last frame -> need more data (unfinished, not bad)")
	tp.Append(stream[len(stream)-2:])
	if ev, err := tp.Next(); err == nil {
		fmt.Printf("remaining bytes arrive -> accept seq=%d payload=%q\n", ev.Frame.Seq, ev.Frame.Payload)
	}
}

func kindName(k chain.EventKind) string {
	if k == chain.EventDuplicate {
		return "duplicate"
	}
	return "frame"
}

func report(scene string, err error) {
	var ce *chain.ChainError
	if errors.As(err, &ce) {
		fmt.Printf("%s -> %s (offset=%d gotSeq=%d wantSeq=%d)\n", scene, ce.Kind, ce.Offset, ce.Got, ce.Want)
	} else {
		fmt.Printf("%s -> %v\n", scene, err)
	}
	fmt.Println()
}

func must(err error) {
	if err != nil {
		fail("fatal", err)
	}
}

func fail(where string, err error) {
	fmt.Fprintf(os.Stderr, "%s: %v\n", where, err)
	os.Exit(1)
}
