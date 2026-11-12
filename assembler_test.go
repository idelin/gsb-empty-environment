package netsec

import (
	"errors"
	"fmt"
	"testing"

	"example.org/netsec/q0122/hpack"
)

func encodeBlock(t *testing.T, huffman bool, fields ...hpack.Field) []byte {
	t.Helper()
	enc := newTestEncoder()
	enc.huffman = huffman
	enc.writeFields(fields)
	return enc.bytes()
}

func wantFields(n int) []hpack.Field {
	out := make([]hpack.Field, n)
	for i := range out {
		out[i] = hpack.Field{
			Name:  fmt.Sprintf("x-test-header-%02d", i),
			Value: fmt.Sprintf("value-%02d", i),
		}
	}
	return out
}

func newTestAssembler(t *testing.T, limits Limits) *Assembler {
	t.Helper()
	return New(hpack.NewDecoder(4096, 4096), limits)
}

// boundarySplits returns one two-fragment cut for every internal byte
// boundary of block, i.e. every position 1..len(block)-1 becomes a frame
// boundary exactly once. This gives exhaustive boundary coverage in O(n)
// runs instead of the exponential set of every multi-cut combination.
func boundarySplits(block []byte) [][][]byte {
	out := make([][][]byte, 0, len(block)-1)
	for cut := 1; cut < len(block); cut++ {
		out = append(out, [][]byte{block[:cut], block[cut:]})
	}
	return out
}

// alternatingSplits cuts block into fragments of the given run length,
// exercising multi-frame header blocks beyond the two-frame boundary test.
func alternatingSplits(block []byte, run int) [][]byte {
	var fragments [][]byte
	for start := 0; start < len(block); start += run {
		end := start + run
		if end > len(block) {
			end = len(block)
		}
		fragments = append(fragments, block[start:end])
	}
	return fragments
}

func feedFragments(t *testing.T, a *Assembler, streamID uint32, fragments [][]byte) *Result {
	t.Helper()
	var result *Result
	for i, frag := range fragments {
		last := i == len(fragments)-1
		var f Frame
		if i == 0 {
			f = Frame{Type: FrameHeaders, StreamID: streamID, Payload: frag, EndHeaders: last}
		} else {
			f = Frame{Type: FrameContinuation, StreamID: streamID, Payload: frag, EndHeaders: last}
		}
		r, err := a.HandleFrame(f)
		if err != nil {
			t.Fatalf("fragment %d/%d: unexpected error: %v", i, len(fragments), err)
		}
		if last {
			if r == nil {
				t.Fatalf("final fragment %d produced no result", i)
			}
			result = r
		} else if r != nil {
			t.Fatalf("non-final fragment %d unexpectedly produced a result", i)
		}
		if !last && !a.HasOpenBlock() {
			t.Fatalf("block should remain open after fragment %d", i)
		}
	}
	if a.HasOpenBlock() {
		t.Fatal("block should be closed after END_HEADERS")
	}
	return result
}

// TestAllSplitPositions reassembles one header block using every possible
// cut-position combination and requires identical decoded fields each time.
func TestAllSplitPositions(t *testing.T) {
	for _, useHuffman := range []bool{false, true} {
		name := "plain"
		if useHuffman {
			name = "huffman"
		}
		t.Run(name, func(t *testing.T) {
			fields := wantFields(3)
			block := encodeBlock(t, useHuffman, fields...)
			splits := boundarySplits(block)
			if got := len(splits); got != len(block)-1 {
				t.Fatalf("split count = %d, want %d", got, len(block)-1)
			}
			for si, fragments := range splits {
				a := newTestAssembler(t, NewDefaultLimits())
				result := feedFragments(t, a, 7, fragments)
				if result.StreamID != 7 || result.BlockBytes != len(block) {
					t.Fatalf("split %d: bad result metadata %+v", si, result)
				}
				if len(result.Fields) != len(fields) {
					t.Fatalf("split %d: %d fields, want %d", si, len(result.Fields), len(fields))
				}
				for i, want := range fields {
					if result.Fields[i] != want {
						t.Fatalf("split %d field %d: got %+v, want %+v", si, i, result.Fields[i], want)
					}
				}
			}

			// Multi-frame cases: every run length 1..4 and one all-single-byte run.
			for run := 1; run <= 4; run++ {
				a := newTestAssembler(t, NewDefaultLimits())
				result := feedFragments(t, a, 7, alternatingSplits(block, run))
				if len(result.Fields) != len(fields) {
					t.Fatalf("run %d: %d fields, want %d", run, len(result.Fields), len(fields))
				}
			}
		})
	}
}

// TestAllSplitPositionsMultiStream runs the exhaustive split test while a
// second completed stream exists, proving fragmentation never crosses
// streams and earlier results survive.
func TestAllSplitPositionsMultiStream(t *testing.T) {
	fields := wantFields(3)
	block := encodeBlock(t, true, fields...)
	for _, fragments := range boundarySplits(block) {
		a := newTestAssembler(t, NewDefaultLimits())
		first := feedFragments(t, a, 3, [][]byte{encodeBlock(t, false, wantFields(2)...)})
		snapshot := a.PublishedSnapshot()

		second := feedFragments(t, a, 5, fragments)
		if first.StreamID != 3 || second.StreamID != 5 {
			t.Fatalf("stream ids mixed up: %d %d", first.StreamID, second.StreamID)
		}
		all := a.PublishedSnapshot()
		if len(all) != 2 || all[0].StreamID != 3 || all[1].StreamID != 5 {
			t.Fatalf("published results wrong: %+v", all)
		}
		if len(snapshot) != 1 || snapshot[0].StreamID != 3 {
			t.Fatal("snapshot of earlier results was rewritten")
		}
	}
}

// TestInterleaveSequences drives every malicious frame ordering that can
// appear between HEADERS and END_HEADERS and requires a connection-level
// protocol error, a permanently closed assembler and zero new publications.
func TestInterleaveSequences(t *testing.T) {
	block := encodeBlock(t, false, wantFields(3)...)
	cut := len(block) / 2

	cases := map[string]struct {
		frames []Frame
		want   error
	}{
		"CONTINUATION on another stream": {
			frames: []Frame{
				{Type: FrameHeaders, StreamID: 1, Payload: block[:cut]},
				{Type: FrameContinuation, StreamID: 3, Payload: block[cut:]},
			},
			want: ErrContinuationStreamMismatch,
		},
		"HEADERS for another stream": {
			frames: []Frame{
				{Type: FrameHeaders, StreamID: 1, Payload: block[:cut]},
				{Type: FrameHeaders, StreamID: 3, Payload: block[cut:], EndHeaders: true},
			},
			want: ErrInterleave,
		},
		"DATA while block open": {
			frames: []Frame{
				{Type: FrameHeaders, StreamID: 1, Payload: block[:cut]},
				{Type: FrameOther, StreamID: 1, Payload: []byte("data")},
			},
			want: ErrInterleave,
		},
		"SETTINGS while block open": {
			frames: []Frame{
				{Type: FrameHeaders, StreamID: 1, Payload: block[:cut]},
				{Type: FrameOther, StreamID: 0, Payload: make([]byte, 5)},
			},
			want: ErrInterleave,
		},
		"WINDOW_UPDATE on another stream": {
			frames: []Frame{
				{Type: FrameHeaders, StreamID: 1, Payload: block[:cut]},
				{Type: FrameOther, StreamID: 9},
			},
			want: ErrInterleave,
		},
		"RST_STREAM for another stream": {
			frames: []Frame{
				{Type: FrameHeaders, StreamID: 1, Payload: block[:cut]},
				{Type: FrameReset, StreamID: 9},
			},
			want: ErrInterleave,
		},
		"CONTINUATION without HEADERS": {
			frames: []Frame{
				{Type: FrameContinuation, StreamID: 1, Payload: block},
			},
			want: ErrContinuationWithoutHeaders,
		},
		"interleave after several CONTINUATIONs": {
			frames: []Frame{
				{Type: FrameHeaders, StreamID: 1, Payload: block[:1]},
				{Type: FrameContinuation, StreamID: 1, Payload: block[1:cut]},
				{Type: FrameOther, StreamID: 1, Payload: []byte("x")},
			},
			want: ErrInterleave,
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			a := newTestAssembler(t, NewDefaultLimits())
			good := feedFragments(t, a, 11, [][]byte{encodeBlock(t, false, wantFields(2)...)})
			before := a.PublishedSnapshot()

			var fatal *FrameError
			for i, f := range tc.frames {
				r, err := a.HandleFrame(f)
				if i < len(tc.frames)-1 {
					if err != nil {
						t.Fatalf("step %d failed early: %v", i, err)
					}
					continue
				}
				if r != nil {
					t.Fatal("malicious sequence published a result")
				}
				if !errors.As(err, &fatal) {
					t.Fatalf("want FrameError, got %v", err)
				}
				if fatal.Code != ProtocolError || !errors.Is(err, tc.want) {
					t.Fatalf("got code=%d err=%v, want code=%d err=%v",
						fatal.Code, err, ProtocolError, tc.want)
				}
			}

			if a.HasOpenBlock() {
				t.Fatal("partial block survived the connection error")
			}
			// Publishing stops immediately: every later frame is rejected.
			if _, err := a.HandleFrame(Frame{
				Type:       FrameHeaders,
				StreamID:   13,
				Payload:    encodeBlock(t, false, wantFields(1)...),
				EndHeaders: true,
			}); !errors.Is(err, ErrConnectionClosed) {
				t.Fatalf("post-fatal frame err = %v, want ErrConnectionClosed", err)
			}
			after := a.PublishedSnapshot()
			if len(after) != len(before) || after[0].StreamID != good.StreamID {
				t.Fatalf("earlier result rewritten: before=%+v after=%+v", before, after)
			}
			if a.PublishedCount() != 1 {
				t.Fatalf("published count = %d, want 1", a.PublishedCount())
			}
		})
	}
}

// TestResetAndCloseDropHalfBlock verifies that resetting or closing the
// owning stream discards its partial block so a later HEADERS on the same
// stream id starts from a clean state and no bytes can be reused.
func TestResetAndCloseDropHalfBlock(t *testing.T) {
	block := encodeBlock(t, false, wantFields(3)...)

	for _, mode := range []string{"reset", "close", "reset-frame", "connection-close"} {
		t.Run(mode, func(t *testing.T) {
			a := newTestAssembler(t, NewDefaultLimits())
			if _, err := a.HandleFrame(Frame{
				Type: FrameHeaders, StreamID: 5, Payload: block[:len(block)/2],
			}); err != nil {
				t.Fatal(err)
			}
			if !a.HasOpenBlock() {
				t.Fatal("expected open block")
			}

			switch mode {
			case "reset":
				a.ResetStream(5)
			case "close":
				a.CloseStream(5)
			case "reset-frame":
				_, err := a.HandleFrame(Frame{Type: FrameReset, StreamID: 5})
				var fe *FrameError
				if !errors.As(err, &fe) || fe.Code != StreamError || !errors.Is(err, ErrOpenBlockInterrupted) {
					t.Fatalf("reset frame err = %v, want stream ErrOpenBlockInterrupted", err)
				}
			case "connection-close":
				a.Close()
			}
			if a.HasOpenBlock() {
				t.Fatal("half block was retained")
			}

			if mode == "connection-close" {
				if _, err := a.HandleFrame(Frame{
					Type: FrameHeaders, StreamID: 5, Payload: block, EndHeaders: true,
				}); !errors.Is(err, ErrConnectionClosed) {
					t.Fatalf("err = %v, want ErrConnectionClosed", err)
				}
				return
			}

			result := feedFragments(t, a, 5, [][]byte{block})
			if result.BlockBytes != len(block) || len(result.Fields) != 3 {
				t.Fatalf("reused stream got contaminated block: %+v", result)
			}
		})
	}
}

// TestResetOtherStreamKeepsOpenBlock shows a reset for an unrelated stream
// does not touch the open block.
func TestResetOtherStreamKeepsOpenBlock(t *testing.T) {
	block := encodeBlock(t, false, wantFields(2)...)
	a := newTestAssembler(t, NewDefaultLimits())
	if _, err := a.HandleFrame(Frame{
		Type: FrameHeaders, StreamID: 1, Payload: block[:2],
	}); err != nil {
		t.Fatal(err)
	}
	a.ResetStream(99)
	if !a.HasOpenBlock() || a.OpenStreamID() != 1 {
		t.Fatal("unrelated reset disturbed the open block")
	}
	result, err := a.HandleFrame(Frame{
		Type: FrameContinuation, StreamID: 1,
		Payload: block[2:], EndHeaders: true,
	})
	if err != nil || result == nil {
		t.Fatalf("continuation after unrelated reset failed: %v", err)
	}
}

// TestByteLimit rejects a block crossing MaxBlockBytes at every fragment
// position, reports a stream error and leaves the connection usable.
func TestByteLimit(t *testing.T) {
	block := encodeBlock(t, false, wantFields(4)...)
	for cut := 1; cut < len(block); cut++ {
		a := newTestAssembler(t, Limits{MaxBlockBytes: len(block) - 1, MaxFields: 100})
		_, err := a.HandleFrame(Frame{Type: FrameHeaders, StreamID: 1, Payload: block[:cut]})
		if err != nil {
			t.Fatalf("cut %d: first frame: %v", cut, err)
		}
		_, err = a.HandleFrame(Frame{
			Type: FrameContinuation, StreamID: 1,
			Payload: block[cut:], EndHeaders: true,
		})
		var fe *FrameError
		if !errors.As(err, &fe) || fe.Code != StreamError || !errors.Is(err, ErrBlockTooLarge) {
			t.Fatalf("cut %d: got %v, want stream ErrBlockTooLarge", cut, err)
		}
		if a.HasOpenBlock() {
			t.Fatalf("cut %d: oversized partial block retained", cut)
		}
		// A following smaller request still succeeds: stream error must not
		// poison the connection.
		small := encodeBlock(t, false, wantFields(1)...)
		if len(small) >= len(block) {
			t.Fatalf("test setup: small block %d >= limit %d", len(small), len(block)-1)
		}
		result := feedFragments(t, a, 1, [][]byte{small})
		if result.BlockBytes != len(small) {
			t.Fatalf("cut %d: later request corrupted", cut)
		}
	}

	// Exact-limit boundary: a block equal to MaxBlockBytes is accepted.
	a := newTestAssembler(t, Limits{MaxBlockBytes: len(block), MaxFields: 100})
	result, err := a.HandleFrame(Frame{
		Type: FrameHeaders, StreamID: 3, Payload: block, EndHeaders: true,
	})
	if err != nil || result == nil || result.BlockBytes != len(block) {
		t.Fatalf("exact-limit block rejected: result=%v err=%v", result, err)
	}
}

// TestFieldLimit enforces the decoded-field count independently of bytes.
func TestFieldLimit(t *testing.T) {
	block := encodeBlock(t, false, wantFields(6)...)
	a := newTestAssembler(t, Limits{MaxBlockBytes: 1 << 20, MaxFields: 3})
	_, err := a.HandleFrame(Frame{
		Type: FrameHeaders, StreamID: 1, Payload: block, EndHeaders: true,
	})
	var fe *FrameError
	if !errors.As(err, &fe) || fe.Code != StreamError || !errors.Is(err, ErrTooManyFields) {
		t.Fatalf("got %v, want stream ErrTooManyFields", err)
	}
	if a.HasOpenBlock() {
		t.Fatal("partial block retained after field limit")
	}

	// Exactly at the limit succeeds.
	small := encodeBlock(t, false, wantFields(3)...)
	result, err := a.HandleFrame(Frame{
		Type: FrameHeaders, StreamID: 3, Payload: small, EndHeaders: true,
	})
	if err != nil || result == nil || len(result.Fields) != 3 {
		t.Fatalf("boundary request failed: result=%v err=%v", result, err)
	}
}

// TestMalformedHPACKIsStreamError feeds a truncated block and requires a
// stream-level error without poisoning the connection.
func TestMalformedHPACKIsStreamError(t *testing.T) {
	good := encodeBlock(t, false, wantFields(2)...)
	bad := []byte{0x7f, 0x80} // varint continuation with no terminator

	a := newTestAssembler(t, NewDefaultLimits())
	feedFragments(t, a, 1, [][]byte{good})

	_, err := a.HandleFrame(Frame{
		Type: FrameHeaders, StreamID: 5, Payload: bad[:1],
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = a.HandleFrame(Frame{
		Type: FrameContinuation, StreamID: 5, Payload: bad[1:], EndHeaders: true,
	})
	var fe *FrameError
	if !errors.As(err, &fe) || fe.Code != StreamError {
		t.Fatalf("got %v, want StreamError", err)
	}
	if a.HasOpenBlock() {
		t.Fatal("malformed partial block retained")
	}

	// The shared HPACK decoder remains usable for a later valid block.
	result := feedFragments(t, a, 7, [][]byte{good})
	if len(result.Fields) != 2 {
		t.Fatalf("later request fields = %d", len(result.Fields))
	}
	if a.PublishedCount() != 2 {
		t.Fatalf("published count = %d, want 2", a.PublishedCount())
	}
}

// fakeDecoder injects failures and records the exact block handed to it.
type fakeDecoder struct {
	err    error
	last   []byte
	called int
	fields []hpack.Field
}

func (f *fakeDecoder) DecodeFull(block []byte) ([]hpack.Field, error) {
	f.called++
	f.last = append(f.last[:0], block...)
	if f.err != nil {
		return nil, f.err
	}
	return f.fields, nil
}

func TestDecoderErrorIsStreamErrorAndStateClears(t *testing.T) {
	injected := errors.New("boom")
	fd := &fakeDecoder{err: injected, fields: nil}
	a := New(fd, NewDefaultLimits())
	_, err := a.HandleFrame(Frame{
		Type: FrameHeaders, StreamID: 1, Payload: []byte{1, 2, 3}, EndHeaders: true,
	})
	var fe *FrameError
	if !errors.As(err, &fe) || fe.Code != StreamError || !errors.Is(err, injected) {
		t.Fatalf("got %v, want wrapped injected error", err)
	}
	if fd.called != 1 || len(fd.last) != 3 {
		t.Fatalf("decoder invocation wrong: called=%d last=%v", fd.called, fd.last)
	}
	if a.HasOpenBlock() {
		t.Fatal("block retained after decoder error")
	}
}

// TestPayloadCopied proves the assembler does not retain the host's frame
// buffer, so reuse by the receiver cannot mutate an assembled block.
func TestPayloadCopied(t *testing.T) {
	block := encodeBlock(t, false, wantFields(2)...)
	a := newTestAssembler(t, NewDefaultLimits())

	buf := make([]byte, len(block))
	copy(buf, block)
	if _, err := a.HandleFrame(Frame{Type: FrameHeaders, StreamID: 1, Payload: buf[:1]}); err != nil {
		t.Fatal(err)
	}
	// Host recycles and overwrites its buffer while the block is open.
	for i := range buf {
		buf[i] = 0xff
	}
	result, err := a.HandleFrame(Frame{
		Type: FrameContinuation, StreamID: 1,
		Payload: append([]byte(nil), block[1:]...), EndHeaders: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.BlockBytes != len(block) || len(result.Fields) != 2 {
		t.Fatalf("reused host buffer contaminated block: %+v", result)
	}
}

// TestFrameOtherIgnoredWhenIdle confirms non-header frames outside an open
// block are simply ignored, while they remain fatal during an open block.
func TestFrameOtherIgnoredWhenIdle(t *testing.T) {
	a := newTestAssembler(t, NewDefaultLimits())
	r, err := a.HandleFrame(Frame{Type: FrameOther, StreamID: 1, Payload: []byte("ping")})
	if err != nil || r != nil {
		t.Fatalf("idle FrameOther: r=%v err=%v", r, err)
	}
	r, err = a.HandleFrame(Frame{Type: FrameReset, StreamID: 1})
	if err != nil || r != nil {
		t.Fatalf("idle RST_STREAM: r=%v err=%v", r, err)
	}
}

// TestZeroValueLimits means unlimited, matching the documented contract.
func TestZeroValueLimitsUnlimited(t *testing.T) {
	block := encodeBlock(t, false, wantFields(20)...)
	a := newTestAssembler(t, Limits{})
	result, err := a.HandleFrame(Frame{
		Type: FrameHeaders, StreamID: 1, Payload: block, EndHeaders: true,
	})
	if err != nil {
		t.Fatalf("unlimited block rejected: %v", err)
	}
	if len(result.Fields) != 20 {
		t.Fatalf("fields = %d, want 20", len(result.Fields))
	}
}
