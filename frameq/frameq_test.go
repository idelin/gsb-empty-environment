package frameq

import (
	"bytes"
	"errors"
	"fmt"
	"testing"
)

func mustScheduler(t *testing.T, maxFrame int, quantum map[int]int) *Scheduler {
	t.Helper()
	s, err := NewScheduler(maxFrame, quantum)
	if err != nil {
		t.Fatalf("NewScheduler: %v", err)
	}
	return s
}

func mustEnqueue(t *testing.T, s *Scheduler, m *Message) {
	t.Helper()
	if err := s.Enqueue(m); err != nil {
		t.Fatalf("Enqueue(%d): %v", m.ID, err)
	}
}

// drainAll emits every frame until the scheduler is empty.
func drainAll(s *Scheduler) []*Frame {
	var frames []*Frame
	for {
		f, ok := s.NextFrame()
		if !ok {
			return frames
		}
		frames = append(frames, f)
	}
}

func TestFrameEncodeParseRoundTrip(t *testing.T) {
	in := &Frame{Fragments: []Fragment{
		{MsgID: 1, Offset: 0, Total: 10, Data: []byte("hello")},
		{MsgID: 2, Offset: 3, Total: 8, Last: true, Data: []byte("world")},
	}}
	enc := in.Encode()
	if got, want := len(enc), in.Len(); got != want {
		t.Fatalf("encoded len %d, want %d", got, want)
	}
	out, n, err := ParseFrame(enc, 1024)
	if err != nil {
		t.Fatalf("ParseFrame: %v", err)
	}
	if n != len(enc) {
		t.Fatalf("consumed %d, want %d", n, len(enc))
	}
	if len(out.Fragments) != 2 || string(out.Fragments[0].Data) != "hello" ||
		string(out.Fragments[1].Data) != "world" || !out.Fragments[1].Last {
		t.Fatalf("round trip mismatch: %+v", out.Fragments)
	}
}

func TestParserArbitraryChunking(t *testing.T) {
	f1 := &Frame{Fragments: []Fragment{{MsgID: 1, Total: 5, Last: true, Data: []byte("hello")}}}
	f2 := &Frame{Fragments: []Fragment{{MsgID: 2, Total: 5, Last: true, Data: []byte("world")}}}
	stream := append(f1.Encode(), f2.Encode()...)

	p := NewParser(1024)
	var got []*Frame
	// Feed one byte at a time: every Next before the final byte of a
	// frame must report ErrIncomplete, never a corruption error.
	for i, b := range stream {
		p.Feed([]byte{b})
		for {
			f, err := p.Next()
			if errors.Is(err, ErrIncomplete) {
				break
			}
			if err != nil {
				t.Fatalf("byte %d: unexpected corruption error: %v", i, err)
			}
			got = append(got, f)
		}
	}
	if len(got) != 2 {
		t.Fatalf("got %d frames, want 2", len(got))
	}
	if string(got[0].Fragments[0].Data) != "hello" || string(got[1].Fragments[0].Data) != "world" {
		t.Fatalf("payload mismatch")
	}
	if p.Buffered() != 0 {
		t.Fatalf("parser holds %d leftover bytes", p.Buffered())
	}
}

func TestParserDistinguishesIncompleteFromCorrupt(t *testing.T) {
	f := &Frame{Fragments: []Fragment{{MsgID: 1, Total: 3, Last: true, Data: []byte("abc")}}}
	enc := f.Encode()

	// A truncated prefix is incomplete, not corrupt.
	if _, _, err := ParseFrame(enc[:len(enc)-1], 1024); !errors.Is(err, ErrIncomplete) {
		t.Fatalf("truncated frame: got %v, want ErrIncomplete", err)
	}
	// Bad magic is corrupt.
	bad := bytes.Clone(enc)
	bad[0] ^= 0xFF
	if _, _, err := ParseFrame(bad, 1024); !errors.Is(err, ErrBadMagic) {
		t.Fatalf("bad magic: got %v, want ErrBadMagic", err)
	}
	// Corrupted payload fails the CRC.
	bad = bytes.Clone(enc)
	bad[len(bad)-6] ^= 0xFF
	if _, _, err := ParseFrame(bad, 1024); !errors.Is(err, ErrBadCRC) {
		t.Fatalf("bad crc: got %v, want ErrBadCRC", err)
	}
}

func TestParserChecksLengthQuotaBeforeAllocating(t *testing.T) {
	// A header declaring a 4 GiB body must be rejected against the quota
	// without allocating the body.
	huge := make([]byte, 6)
	huge[0], huge[1] = 0xFA, 0x5C
	huge[2], huge[3], huge[4], huge[5] = 0xFF, 0xFF, 0xFF, 0xFF
	if _, _, err := ParseFrame(huge, 256); !errors.Is(err, ErrFrameTooBig) {
		t.Fatalf("got %v, want ErrFrameTooBig", err)
	}
	// A declared 1 MiB body within a 2 MiB quota reports incomplete instead.
	huge[2], huge[3], huge[4], huge[5] = 0x00, 0x10, 0x00, 0x00
	if _, _, err := ParseFrame(huge, 2<<20); !errors.Is(err, ErrIncomplete) {
		t.Fatalf("got %v, want ErrIncomplete", err)
	}
}

func TestSchedulerHeaderOverheadCountsTowardLimit(t *testing.T) {
	const maxFrame = 64
	s := mustScheduler(t, maxFrame, map[int]int{1: 1000})
	mustEnqueue(t, s, &Message{ID: 1, Priority: 1, Data: bytes.Repeat([]byte("x"), 1000)})
	for {
		f, ok := s.NextFrame()
		if !ok {
			return
		}
		if f.Len() > maxFrame {
			t.Fatalf("frame is %d bytes, limit %d", f.Len(), maxFrame)
		}
		if len(f.Encode()) > maxFrame {
			t.Fatalf("encoded frame exceeds limit")
		}
	}
}

func TestSchedulerExactFillEmitsNoExtraFrame(t *testing.T) {
	// One frame fits exactly: header + one fragment header + payload.
	const maxFrame = FrameHeaderSize + FragHeaderSize + 50
	s := mustScheduler(t, maxFrame, map[int]int{1: 1000})
	mustEnqueue(t, s, &Message{ID: 1, Priority: 1, Data: bytes.Repeat([]byte("a"), 50)})

	f, ok := s.NextFrame()
	if !ok {
		t.Fatal("expected one frame")
	}
	if f.Len() != maxFrame {
		t.Fatalf("frame is %d bytes, want exact fill %d", f.Len(), maxFrame)
	}
	if !f.Fragments[0].Last {
		t.Fatal("single fragment should be marked last")
	}
	if _, ok := s.NextFrame(); ok {
		t.Fatal("exact fill must not produce an extra empty frame")
	}
}

func TestSchedulerFairnessNoStarvation(t *testing.T) {
	// A huge high-priority message competes with small low-priority ones.
	const maxFrame = 128
	s := mustScheduler(t, maxFrame, map[int]int{10: 80, 1: 24})
	big := &Message{ID: 1, Priority: 10, Data: bytes.Repeat([]byte("B"), 100*maxFrame)}
	mustEnqueue(t, s, big)
	var small []*Message
	for i := 0; i < 5; i++ {
		m := &Message{ID: uint32(100 + i), Priority: 1, Data: []byte(fmt.Sprintf("small-%d", i))}
		small = append(small, m)
		mustEnqueue(t, s, m)
	}

	r := NewReassembler()
	done := map[uint32]bool{}
	frames := 0
	for len(done) < len(small) {
		f, ok := s.NextFrame()
		if !ok {
			t.Fatal("scheduler empty before small messages completed")
		}
		frames++
		if frames > 10 {
			t.Fatalf("small messages starved: still pending after %d frames", frames)
		}
		for _, fr := range f.Fragments {
			msg, err := r.Add(fr)
			if err != nil {
				t.Fatal(err)
			}
			if msg != nil && fr.MsgID != big.ID {
				done[fr.MsgID] = true
			}
		}
	}
	t.Logf("all %d small messages completed within %d frames while a %d-byte high-priority message was pending",
		len(small), frames, len(big.Data))
}

func TestSchedulerPriorityOrderWithinFrame(t *testing.T) {
	const maxFrame = 128
	s := mustScheduler(t, maxFrame, map[int]int{10: 40, 1: 40})
	mustEnqueue(t, s, &Message{ID: 1, Priority: 1, Data: bytes.Repeat([]byte("L"), 30)})
	mustEnqueue(t, s, &Message{ID: 2, Priority: 10, Data: bytes.Repeat([]byte("H"), 30)})
	f, ok := s.NextFrame()
	if !ok {
		t.Fatal("expected a frame")
	}
	if len(f.Fragments) != 2 || f.Fragments[0].MsgID != 2 || f.Fragments[1].MsgID != 1 {
		t.Fatalf("high priority should be served first: %+v", f.Fragments)
	}
}

func TestSchedulerCancelReleasesQuota(t *testing.T) {
	const maxFrame = 64
	s := mustScheduler(t, maxFrame, map[int]int{5: 32, 1: 32})
	doomed := &Message{ID: 1, Priority: 5, Data: bytes.Repeat([]byte("D"), 10000)}
	mustEnqueue(t, s, doomed)
	mustEnqueue(t, s, &Message{ID: 2, Priority: 1, Data: []byte("keep-me")})

	// Send one frame: some fragments of msg 1 are now irrevocably sent.
	f, ok := s.NextFrame()
	if !ok || f.Fragments[0].MsgID != 1 {
		t.Fatal("expected first frame to carry msg 1")
	}
	sentBefore := doomed.Sent()
	if sentBefore == 0 {
		t.Fatal("expected partial progress on msg 1")
	}

	if !s.Cancel(1) {
		t.Fatal("cancel of partially sent message should succeed")
	}
	if s.Pending() != len("keep-me") {
		t.Fatalf("pending = %d, want %d: canceled bytes must release quota", s.Pending(), len("keep-me"))
	}
	if doomed.Sent() != sentBefore {
		t.Fatal("sent byte count must not change on cancel")
	}
	if s.Cancel(1) {
		t.Fatal("double cancel should report false")
	}
	if s.Cancel(999) {
		t.Fatal("cancel of unknown id should report false")
	}

	// Only msg 2 remains; its data must survive intact.
	r := NewReassembler()
	var got []byte
	for _, f := range drainAll(s) {
		for _, fr := range f.Fragments {
			if fr.MsgID == 1 {
				t.Fatal("canceled message emitted after cancel")
			}
			msg, err := r.Add(fr)
			if err != nil {
				t.Fatal(err)
			}
			got = append(got, msg...)
		}
	}
	if string(got) != "keep-me" {
		t.Fatalf("reassembled %q, want %q", got, "keep-me")
	}
}

func TestSchedulerFragmentationAndReassembly(t *testing.T) {
	const maxFrame = 96
	s := mustScheduler(t, maxFrame, map[int]int{3: 48, 1: 24})
	payloads := map[uint32][]byte{
		1: bytes.Repeat([]byte("a"), 500),
		2: bytes.Repeat([]byte("b"), 7),
		3: bytes.Repeat([]byte("c"), 1000),
	}
	mustEnqueue(t, s, &Message{ID: 1, Priority: 3, Data: payloads[1]})
	mustEnqueue(t, s, &Message{ID: 2, Priority: 1, Data: payloads[2]})
	mustEnqueue(t, s, &Message{ID: 3, Priority: 3, Data: payloads[3]})

	// Pipe every frame through the streaming parser in odd-sized chunks,
	// then reassemble.
	p := NewParser(maxFrame)
	r := NewReassembler()
	done := map[uint32][]byte{}
	for _, f := range drainAll(s) {
		enc := f.Encode()
		for len(enc) > 0 {
			n := min(13, len(enc))
			p.Feed(enc[:n])
			enc = enc[n:]
			for {
				pf, err := p.Next()
				if errors.Is(err, ErrIncomplete) {
					break
				}
				if err != nil {
					t.Fatal(err)
				}
				for _, fr := range pf.Fragments {
					msg, err := r.Add(fr)
					if err != nil {
						t.Fatal(err)
					}
					if msg != nil {
						done[fr.MsgID] = msg
					}
				}
			}
		}
	}
	if len(done) != 3 {
		t.Fatalf("completed %d messages, want 3", len(done))
	}
	for id, want := range payloads {
		if !bytes.Equal(done[id], want) {
			t.Fatalf("msg %d: reassembled %d bytes, want %d", id, len(done[id]), len(want))
		}
	}
}
