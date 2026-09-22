package framepack

import (
	"bytes"
	"errors"
	"fmt"
	"testing"
)

func mustScheduler(t *testing.T, maxFrame int, quanta []int, quota int) *Scheduler {
	t.Helper()
	s, err := NewScheduler(maxFrame, quanta, quota)
	if err != nil {
		t.Fatalf("NewScheduler: %v", err)
	}
	return s
}

func drain(t *testing.T, s *Scheduler) []Frame {
	t.Helper()
	var frames []Frame
	for {
		f, ok := s.NextFrame()
		if !ok {
			return frames
		}
		frames = append(frames, f)
		if len(frames) > 100000 {
			t.Fatal("scheduler did not terminate")
		}
	}
}

func TestHeaderOverheadCountsTowardFrameLimit(t *testing.T) {
	maxFrame := HeaderSize + 4
	s := mustScheduler(t, maxFrame, []int{1 << 20}, 1<<20)
	if _, err := s.Enqueue(0, bytes.Repeat([]byte("ab"), 4)); err != nil {
		t.Fatal(err)
	}
	frames := drain(t, s)
	if len(frames) != 2 {
		t.Fatalf("want 2 frames, got %d", len(frames))
	}
	for _, f := range frames {
		if got := HeaderSize + len(f.Payload); got > maxFrame {
			t.Fatalf("frame wire size %d exceeds maxFrame %d", got, maxFrame)
		}
		if len(f.Payload) > 4 {
			t.Fatalf("payload %d exceeds capacity 4", len(f.Payload))
		}
	}
}

func TestExactFitProducesNoExtraFrame(t *testing.T) {
	cap := 10
	s := mustScheduler(t, HeaderSize+cap, []int{1 << 20}, 1<<20)
	if _, err := s.Enqueue(0, bytes.Repeat([]byte("x"), cap)); err != nil {
		t.Fatal(err)
	}
	f, ok := s.NextFrame()
	if !ok {
		t.Fatal("expected a frame")
	}
	if !f.Start() || !f.End() {
		t.Fatalf("single-frame message must carry START|END, got flags %#x", f.Flags)
	}
	if len(f.Payload) != cap {
		t.Fatalf("payload = %d, want %d", len(f.Payload), cap)
	}
	if _, ok := s.NextFrame(); ok {
		t.Fatal("exact-fit message produced an extra frame")
	}
}

func TestFragmentationBoundary(t *testing.T) {
	cap := 10
	s := mustScheduler(t, HeaderSize+cap, []int{1 << 20}, 1<<20)
	if _, err := s.Enqueue(0, bytes.Repeat([]byte("y"), 2*cap)); err != nil {
		t.Fatal(err)
	}
	frames := drain(t, s)
	if len(frames) != 2 {
		t.Fatalf("20 bytes into capacity 10: want 2 frames, got %d", len(frames))
	}
	if !frames[0].Start() || frames[0].End() {
		t.Fatalf("first fragment flags %#x", frames[0].Flags)
	}
	if frames[1].Start() || !frames[1].End() {
		t.Fatalf("last fragment flags %#x", frames[1].Flags)
	}
}

func TestPriorityOrderWithFairQuota(t *testing.T) {
	// Class 0 (high) quantum 8, class 1 (low) quantum 8, capacity 4.
	s := mustScheduler(t, HeaderSize+4, []int{8, 8}, 1<<20)
	low, err := s.Enqueue(1, []byte("LLLL"))
	if err != nil {
		t.Fatal(err)
	}
	high, err := s.Enqueue(0, bytes.Repeat([]byte("H"), 16))
	if err != nil {
		t.Fatal(err)
	}
	var order []uint32
	for {
		f, ok := s.NextFrame()
		if !ok {
			break
		}
		order = append(order, f.MsgID)
	}
	// High priority goes first, but after spending its 8-byte round
	// quantum the low message gets its turn before the high message
	// continues.
	want := []uint32{high, high, low, high, high}
	if fmt.Sprint(order) != fmt.Sprint(want) {
		t.Fatalf("send order %v, want %v", order, want)
	}
}

func TestHighPriorityLargeMessageDoesNotStarveSmall(t *testing.T) {
	s := mustScheduler(t, HeaderSize+16, []int{32, 8}, 1<<20)
	big, err := s.Enqueue(0, bytes.Repeat([]byte("B"), 4096))
	if err != nil {
		t.Fatal(err)
	}
	small, err := s.Enqueue(1, []byte("tiny"))
	if err != nil {
		t.Fatal(err)
	}
	bigEnd, smallEnd := -1, -1
	for i := 0; ; i++ {
		f, ok := s.NextFrame()
		if !ok {
			break
		}
		if f.MsgID == big && f.End() {
			bigEnd = i
		}
		if f.MsgID == small && f.End() {
			smallEnd = i
		}
		if i > 10000 {
			t.Fatal("did not terminate")
		}
	}
	if smallEnd < 0 || bigEnd < 0 {
		t.Fatalf("missing completion: bigEnd=%d smallEnd=%d", bigEnd, smallEnd)
	}
	if smallEnd >= bigEnd {
		t.Fatalf("small message finished at frame %d, after big message at %d", smallEnd, bigEnd)
	}
}

func TestCancelReleasesQuotaSentFragmentsIrrevocable(t *testing.T) {
	quota := 100
	s := mustScheduler(t, HeaderSize+10, []int{1 << 20}, quota)
	id, err := s.Enqueue(0, bytes.Repeat([]byte("m"), 80))
	if err != nil {
		t.Fatal(err)
	}
	// Send 3 fragments (30 bytes); those are irrevocable.
	for i := 0; i < 3; i++ {
		if _, ok := s.NextFrame(); !ok {
			t.Fatal("expected frame")
		}
	}
	if got := s.QueueUsed(0); got != 50 {
		t.Fatalf("queue used = %d, want 50", got)
	}
	released, err := s.Cancel(id)
	if err != nil {
		t.Fatal(err)
	}
	if released != 50 {
		t.Fatalf("released = %d, want 50 unsent bytes", released)
	}
	if got := s.QueueUsed(0); got != 0 {
		t.Fatalf("queue used after cancel = %d, want 0", got)
	}
	// Quota was released: a new 100-byte message fits again.
	if _, err := s.Enqueue(0, bytes.Repeat([]byte("n"), 100)); err != nil {
		t.Fatalf("quota not released: %v", err)
	}
	// Canceling twice fails; already-sent fragments are unaffected.
	if _, err := s.Cancel(id); !errors.Is(err, ErrNoSuchMessage) {
		t.Fatalf("second cancel: %v", err)
	}
}

func TestEnqueueQuotaCheckedUpFront(t *testing.T) {
	s := mustScheduler(t, HeaderSize+10, []int{1 << 20}, 16)
	if _, err := s.Enqueue(0, bytes.Repeat([]byte("z"), 17)); !errors.Is(err, ErrQuotaExceeded) {
		t.Fatalf("want ErrQuotaExceeded, got %v", err)
	}
	if _, err := s.Enqueue(9, []byte("a")); !errors.Is(err, ErrBadPriority) {
		t.Fatalf("want ErrBadPriority, got %v", err)
	}
}

func TestParserArbitraryChunking(t *testing.T) {
	s := mustScheduler(t, HeaderSize+7, []int{1 << 20}, 1<<20)
	msg1 := []byte("hello fragmented world!!")
	msg2 := []byte("second")
	if _, err := s.Enqueue(0, msg1); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Enqueue(0, msg2); err != nil {
		t.Fatal(err)
	}
	var wire []byte
	for _, f := range drain(t, s) {
		wire = append(wire, EncodeFrame(f)...)
	}
	p := NewParser(HeaderSize+7, 1<<20)
	r := NewReassembler(1 << 20)
	var got [][]byte
	for i := 0; i < len(wire); i++ { // feed one byte at a time
		frames, err := p.Feed(wire[i : i+1])
		if err != nil {
			t.Fatalf("byte %d: %v", i, err)
		}
		for _, f := range frames {
			payload, done, err := r.Push(f)
			if err != nil {
				t.Fatal(err)
			}
			if done {
				got = append(got, payload)
			}
		}
	}
	if p.Buffered() != 0 {
		t.Fatalf("parser holds %d leftover bytes", p.Buffered())
	}
	if len(got) != 2 || !bytes.Equal(got[0], msg1) || !bytes.Equal(got[1], msg2) {
		t.Fatalf("reassembled %q, want %q and %q", got, msg1, msg2)
	}
}

func TestParseFrameDistinguishesNeedMoreFromBad(t *testing.T) {
	f := Frame{MsgID: 7, Flags: FlagStart | FlagEnd, TotalLen: 5, Payload: []byte("12345")}
	wire := EncodeFrame(f)
	maxFrame, maxMsg := HeaderSize+16, 1<<20

	for _, n := range []int{0, 3, HeaderSize - 1, HeaderSize, len(wire) - 1} {
		if _, _, err := ParseFrame(wire[:n], maxFrame, maxMsg); !errors.Is(err, ErrNeedMoreData) {
			t.Fatalf("prefix of %d bytes: want ErrNeedMoreData, got %v", n, err)
		}
	}
	got, consumed, err := ParseFrame(wire, maxFrame, maxMsg)
	if err != nil {
		t.Fatal(err)
	}
	if consumed != len(wire) || !bytes.Equal(got.Payload, f.Payload) || got.MsgID != 7 {
		t.Fatalf("decoded %+v consumed %d", got, consumed)
	}

	bad := append([]byte(nil), wire...)
	bad[0], bad[1] = 0xDE, 0xAD
	if _, _, err := ParseFrame(bad, maxFrame, maxMsg); !errors.Is(err, ErrBadData) {
		t.Fatalf("bad magic: want ErrBadData, got %v", err)
	}
	if errors.Is(ParseFrameErr(bad, maxFrame, maxMsg), ErrNeedMoreData) {
		t.Fatal("bad magic misreported as need-more-data")
	}
}

func ParseFrameErr(buf []byte, maxFrame, maxMsg int) error {
	_, _, err := ParseFrame(buf, maxFrame, maxMsg)
	return err
}

func TestLengthQuotaCheckedBeforeAllocation(t *testing.T) {
	// Header claims a payload larger than maxFrame allows.
	f := Frame{MsgID: 1, Flags: FlagStart | FlagEnd, TotalLen: 5, Payload: []byte("12345")}
	wire := EncodeFrame(f)
	if _, _, err := ParseFrame(wire, HeaderSize+4, 1<<20); !errors.Is(err, ErrBadData) {
		t.Fatalf("oversize frame: want ErrBadData, got %v", err)
	}
	// Header announces a total message length beyond the message quota.
	big := EncodeFrame(Frame{MsgID: 2, Flags: FlagStart, TotalLen: 1 << 30, Payload: []byte("x")})
	if _, _, err := ParseFrame(big, HeaderSize+16, 1<<20); !errors.Is(err, ErrBadData) {
		t.Fatalf("oversize message: want ErrBadData, got %v", err)
	}
	// Reassembler rejects an over-quota announcement before allocating.
	r := NewReassembler(8)
	if _, _, err := r.Push(Frame{MsgID: 3, Flags: FlagStart, TotalLen: 100, Payload: []byte("x")}); !errors.Is(err, ErrBadData) {
		t.Fatalf("reassembler quota: want ErrBadData, got %v", err)
	}
	// A truncated frame with a valid header is need-more, not bad.
	if _, _, err := ParseFrame(wire[:len(wire)-1], HeaderSize+16, 1<<20); !errors.Is(err, ErrNeedMoreData) {
		t.Fatalf("truncated payload: want ErrNeedMoreData, got %v", err)
	}
}

func TestParserBadVersionAndFlags(t *testing.T) {
	good := EncodeFrame(Frame{MsgID: 1, Flags: FlagStart | FlagEnd, TotalLen: 1, Payload: []byte("q")})
	badVer := append([]byte(nil), good...)
	badVer[2] = 99
	if _, err := NewParser(len(good), 1<<20).Feed(badVer); !errors.Is(err, ErrBadData) {
		t.Fatalf("bad version: want ErrBadData, got %v", err)
	}
	badFlags := append([]byte(nil), good...)
	badFlags[3] = 0x80
	if _, err := NewParser(len(good), 1<<20).Feed(badFlags); !errors.Is(err, ErrBadData) {
		t.Fatalf("bad flags: want ErrBadData, got %v", err)
	}
}

func TestRoundTripInterleaved(t *testing.T) {
	maxFrame := HeaderSize + 12
	s := mustScheduler(t, maxFrame, []int{24, 12, 6}, 1<<20)
	msgs := map[uint32][]byte{}
	for i, payload := range [][]byte{
		bytes.Repeat([]byte("a"), 100),
		bytes.Repeat([]byte("b"), 30),
		bytes.Repeat([]byte("c"), 5),
	} {
		id, err := s.Enqueue(i, payload)
		if err != nil {
			t.Fatal(err)
		}
		msgs[id] = payload
	}
	p := NewParser(maxFrame, 1<<20)
	r := NewReassembler(1 << 20)
	got := map[uint32][]byte{}
	var wire []byte
	for {
		f, ok := s.NextFrame()
		if !ok {
			break
		}
		wire = append(wire, EncodeFrame(f)...)
		// Feed in awkward 5-byte chunks as the stream grows.
		for len(wire) >= 5 {
			frames, err := p.Feed(wire[:5])
			if err != nil {
				t.Fatal(err)
			}
			wire = wire[5:]
			for _, fr := range frames {
				payload, done, err := r.Push(fr)
				if err != nil {
					t.Fatal(err)
				}
				if done {
					got[fr.MsgID] = payload
				}
			}
		}
	}
	frames, err := p.Feed(wire)
	if err != nil {
		t.Fatal(err)
	}
	for _, fr := range frames {
		payload, done, err := r.Push(fr)
		if err != nil {
			t.Fatal(err)
		}
		if done {
			got[fr.MsgID] = payload
		}
	}
	for id, want := range msgs {
		if !bytes.Equal(got[id], want) {
			t.Fatalf("message %d: got %d bytes, want %d", id, len(got[id]), len(want))
		}
	}
}
