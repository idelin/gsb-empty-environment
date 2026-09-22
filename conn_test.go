package framemux

import (
	"bytes"
	"errors"
	"testing"
)

// Per-stream quotas are independent of each other and of the global quota;
// a stalled stream unblocks after Recv frees budget.
func TestBackpressurePerStreamAndGlobal(t *testing.T) {
	cfg := Config{MaxPayload: 64, MaxStreamBuffer: 100, MaxTotalBuffer: 400}
	a, b := NewConn(cfg), NewConn(cfg)
	payload := bytes.Repeat([]byte{'a'}, 30) // charge = 62 per frame

	for i := 0; i < 2; i++ {
		if err := a.Send(1, payload); err != nil {
			t.Fatal(err)
		}
	}
	if err := a.Send(2, payload); err != nil {
		t.Fatal(err)
	}
	wire := a.TakeOutput()

	// First stream-1 frame fits (62 <= 100); the second exceeds the
	// per-stream quota (124 > 100) and stalls the parser.
	if _, err := b.Write(wire); !errors.Is(err, ErrBackpressure) {
		t.Fatalf("got %v, want ErrBackpressure", err)
	}
	f, ok := b.Recv()
	if !ok || f.Stream != 1 {
		t.Fatalf("expected first stream-1 frame, got %+v ok=%v", f, ok)
	}
	// Recv freed quota: the stalled frame and the stream-2 frame flow.
	f2, ok := b.Recv()
	if !ok || f2.Stream != 1 {
		t.Fatalf("expected second stream-1 frame, got %+v ok=%v", f2, ok)
	}
	f3, ok := b.Recv()
	if !ok || f3.Stream != 2 {
		t.Fatalf("expected stream-2 frame, got %+v ok=%v", f3, ok)
	}
	if _, ok := b.Recv(); ok {
		t.Fatal("unexpected extra frame")
	}
}

// Pause/Resume preserves an incomplete escape sequence and in-flight epoch
// negotiation; held frames are delivered on Resume.
func TestPauseResumePreservesEscapeAndNegotiation(t *testing.T) {
	cfg := Config{GraceFrames: 4}
	a, b := NewConn(cfg), NewConn(cfg)
	b.Pause(1)

	payload := []byte{0x7D, 0x7E, 0x5A} // forces escape sequences on the wire
	if err := a.Send(1, payload); err != nil {
		t.Fatal(err)
	}
	a.ProposeEpoch(1)
	wire := a.TakeOutput()
	// wire = dataFrame(epoch 0) ++ proposeFrame; split the data frame right
	// after its first escape byte.
	dlen := 0
	for i := 2; i < len(wire); i++ { // skip first delimiter, find next
		if wire[i] == Magic0 && i+1 < len(wire) && wire[i+1] == Magic1 {
			dlen = i
			break
		}
	}
	if dlen == 0 {
		t.Fatal("frame boundary not found")
	}
	dataFrame, propose := wire[:dlen], wire[dlen:]
	cut := bytes.IndexByte(dataFrame, configForEpoch(0).Esc) + 1
	if cut <= 0 {
		t.Fatal("no escape byte in data frame")
	}

	// Chunk 1 ends with a dangling escape while the stream is paused.
	if _, err := b.Write(dataFrame[:cut]); err != nil {
		t.Fatalf("chunk1: %v", err)
	}
	if !b.p.escPending || b.p.state != stPayload {
		t.Fatalf("escape state not pending mid-frame: %+v", b.p.state)
	}
	// Chunk 2 completes the data frame and carries the epoch proposal.
	if _, err := b.Write(append(dataFrame[cut:], propose...)); err != nil {
		t.Fatalf("chunk2: %v", err)
	}
	if b.Epoch() != 1 {
		t.Fatalf("negotiation did not complete while paused: epoch=%d", b.Epoch())
	}
	if _, ok := b.Recv(); ok {
		t.Fatal("paused stream must not deliver")
	}
	// The confirm is queued for the peer; the switch happened at a frame
	// boundary, after the old-epoch data frame.
	a.Write(b.TakeOutput())
	if a.Epoch() != 1 {
		t.Fatalf("initiator did not switch: epoch=%d", a.Epoch())
	}

	b.Resume(1)
	f, ok := b.Recv()
	if !ok || !bytes.Equal(f.Payload, payload) {
		t.Fatalf("held frame lost or corrupted: %+v ok=%v", f, ok)
	}
	// Post-switch traffic uses the new epoch.
	if err := a.Send(1, []byte("new epoch")); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Write(a.TakeOutput()); err != nil {
		t.Fatalf("post-switch: %v", err)
	}
	f, ok = b.Recv()
	if !ok || !bytes.Equal(f.Payload, []byte("new epoch")) {
		t.Fatalf("post-switch frame: %+v ok=%v", f, ok)
	}
}

// The switch confirmation may be interleaved with old-epoch tail frames;
// both are decoded with their own epoch's rules and delivered in order.
func TestEpochSwitchWithTailFrames(t *testing.T) {
	cfg := Config{GraceFrames: 4}
	b := NewConn(cfg)

	// B receives a proposal and switches; its confirm goes out with the old
	// epoch's encoding.
	propose := AppendFrame(nil, Frame{Type: TypePropose, Epoch: 0, Payload: []byte{1}})
	if _, err := b.Write(propose); err != nil {
		t.Fatalf("propose: %v", err)
	}
	if b.Epoch() != 1 {
		t.Fatalf("responder did not switch: epoch=%d", b.Epoch())
	}
	if _, err := NewConn(cfg).Write(b.TakeOutput()); err != nil {
		t.Fatalf("confirm frame is not valid old-epoch encoding: %v", err)
	}

	// Tail frames encoded with epoch 0 (one contains both epochs' escape
	// bytes), followed by a new-epoch frame.
	p1 := []byte{0x7B, 0x7D, 'a'}
	p2 := []byte("tail-two")
	p3 := []byte("new-one")
	var wire []byte
	wire = AppendFrame(wire, Frame{Type: TypeData, Stream: 1, Seq: 0, Epoch: 0, Payload: p1})
	wire = AppendFrame(wire, Frame{Type: TypeData, Stream: 1, Seq: 1, Epoch: 0, Payload: p2})
	wire = AppendFrame(wire, Frame{Type: TypeData, Stream: 1, Seq: 2, Epoch: 1, Payload: p3})

	// Feed in awkward 7-byte chunks to straddle frame and escape boundaries.
	for i := 0; i < len(wire); i += 7 {
		end := i + 7
		if end > len(wire) {
			end = len(wire)
		}
		if _, err := b.Write(wire[i:end]); err != nil {
			t.Fatalf("chunk %d: %v", i/7, err)
		}
	}
	fs := recvAll(b)
	if len(fs) != 3 {
		t.Fatalf("got %d frames, want 3: %+v", len(fs), fs)
	}
	for i, want := range [][]byte{p1, p2, p3} {
		if !bytes.Equal(fs[i].Payload, want) {
			t.Fatalf("frame %d: got %q want %q", i, fs[i].Payload, want)
		}
	}
	if st := b.Stats(); st.BadFrames != 0 || st.Duplicates != 0 {
		t.Fatalf("tail frames mishandled: %+v", st)
	}
}

// A corrupt frame on one stream neither breaks the connection nor disturbs
// other streams.
func TestErrorIsolationBetweenStreams(t *testing.T) {
	a, b := NewConn(Config{}), NewConn(Config{})
	mk := func(stream, seq uint8, payload string) []byte {
		return AppendFrame(nil, Frame{Type: TypeData, Stream: stream, Seq: seq, Epoch: 0, Payload: []byte(payload)})
	}
	bad := mk(1, 0, "AAAA")
	bad[8] ^= 0x01 // flip a payload byte: CRC fails, structure intact

	var wire []byte
	wire = append(wire, mk(2, 0, "s2-first")...)
	wire = append(wire, bad...)
	wire = append(wire, mk(2, 1, "s2-second")...)
	wire = append(wire, mk(1, 1, "s1-recovered")...)

	if _, err := b.Write(wire); !errors.Is(err, ErrBadData) {
		t.Fatalf("got %v, want ErrBadData", err)
	}
	var got []string
	for _, f := range recvAll(b) {
		got = append(got, string(f.Payload))
	}
	want := []string{"s2-first", "s2-second", "s1-recovered"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
	if st := b.Stats(); st.BadFrames != 1 || st.Duplicates != 0 {
		t.Fatalf("stats: %+v", st)
	}
	_ = a
}

// After a recovery the sender may retransmit already-acknowledged frames;
// the receiver must drop them and never deliver a frame twice.
func TestNoDuplicateDeliveryAfterRecovery(t *testing.T) {
	a, b := NewConn(Config{}), NewConn(Config{})
	for _, m := range []string{"m0", "m1", "m2"} {
		if err := a.Send(1, []byte(m)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := b.Write(a.TakeOutput()); err != nil {
		t.Fatal(err)
	}
	if got := len(recvAll(b)); got != 3 {
		t.Fatalf("got %d frames, want 3", got)
	}

	// B acknowledges everything up to seq 2; A sees the ack.
	b.SendAck(1, 2)
	if _, err := a.Write(b.TakeOutput()); err != nil {
		t.Fatal(err)
	}
	ack, ok := a.Recv()
	if !ok || ack.Type != TypeAck || ack.Stream != 1 || ack.Seq != 2 {
		t.Fatalf("ack: %+v ok=%v", ack, ok)
	}

	// Recovery: the sender retransmits seq 0..2 (already acked) plus the
	// new seq 3..4.
	var wire []byte
	for seq := uint8(0); seq < 5; seq++ {
		wire = AppendFrame(wire, Frame{Type: TypeData, Stream: 1, Seq: seq, Epoch: 0,
			Payload: []byte{byte('0' + seq)}})
	}
	if _, err := b.Write(wire); err != nil {
		t.Fatal(err)
	}
	fs := recvAll(b)
	if len(fs) != 2 {
		t.Fatalf("delivered %d frames after recovery, want 2 (no duplicates)", len(fs))
	}
	if fs[0].Seq != 3 || fs[1].Seq != 4 {
		t.Fatalf("delivered seqs %d,%d, want 3,4", fs[0].Seq, fs[1].Seq)
	}
	if st := b.Stats(); st.Duplicates != 3 {
		t.Fatalf("duplicates dropped: %d, want 3", st.Duplicates)
	}
}
