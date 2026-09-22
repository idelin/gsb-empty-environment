package deltacodec

import (
	"bytes"
	"encoding/binary"
	"hash/crc32"
	"io"
	"math"
	"testing"
)

// ---- helpers ----

type encFunc func(*Encoder)

func buildStream(t *testing.T, fn encFunc) []byte {
	t.Helper()
	var buf bytes.Buffer
	enc := NewEncoder(&buf)
	fn(enc)
	return buf.Bytes()
}

func rawFrame(tag byte, ch uint32, payload []byte) []byte {
	return rawFrameSeq(tag, ch, 0, payload)
}

func rawFrameSeq(tag byte, ch uint32, seq uint64, payload []byte) []byte {
	return rawFrameCRC(tag, ch, seq, payload, true)
}

func rawFrameCRC(tag byte, ch uint32, seq uint64, payload []byte, goodCRC bool) []byte {
	f := []byte{tag}
	f = appendUvarint(f, uint64(ch))
	f = appendUvarint(f, seq)
	f = appendUvarint(f, uint64(len(payload)))
	f = append(f, payload...)
	var crc [4]byte
	v := crc32.ChecksumIEEE(f)
	if !goodCRC {
		v ^= uint32(0xFFFFFFFF)
	}
	binary.LittleEndian.PutUint32(crc[:], v)
	return append(f, crc[:]...)
}

func decodeAll(data []byte, chunkSize int) ([]Sample, error) {
	dec := NewDecoder()
	var out []Sample
	for i := 0; i < len(data); {
		end := i + chunkSize
		if end > len(data) {
			end = len(data)
		}
		dec.Feed(data[i:end])
		i = end
		for {
			smp, err := dec.Decode()
			if err == ErrNeedMoreData {
				break
			}
			if err != nil {
				return out, err
			}
			out = append(out, smp)
		}
	}
	return out, nil
}

func mustStatus(t *testing.T, got Sample, want Status, wantVal int64) {
	t.Helper()
	if got.Status != want {
		t.Fatalf("channel %d: got status %d, want %d", got.Channel, got.Status, want)
	}
	if want == StatusOK && got.Value != wantVal {
		t.Fatalf("channel %d: got value %d, want %d", got.Channel, got.Value, wantVal)
	}
}

// ---- tests ----

func TestRoundTrip(t *testing.T) {
	data := buildStream(t, func(e *Encoder) {
		e.Encode(1, 100)
		e.Encode(2, -7)
		e.Encode(1, 105)     // delta +5
		e.Encode(1, -200000) // negative delta
		e.Encode(2, -7)      // delta 0
		e.Encode(1, math.MaxInt64)
		e.Encode(1, math.MinInt64) // difference overflows -> anchor
		e.Encode(2, 0)
	})
	got, err := decodeAll(data, 128)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	want := []struct {
		ch     uint32
		status Status
		v      int64
	}{
		{1, StatusOK, 100},
		{2, StatusOK, -7},
		{1, StatusOK, 105},
		{1, StatusOK, -200000},
		{2, StatusOK, -7},
		{1, StatusOK, math.MaxInt64},
		{1, StatusOK, math.MinInt64},
		{2, StatusOK, 0},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d samples, want %d", len(got), len(want))
	}
	for i, w := range want {
		if got[i].Channel != w.ch {
			t.Fatalf("sample %d: channel got %d want %d", i, got[i].Channel, w.ch)
		}
		mustStatus(t, got[i], w.status, w.v)
	}
}

func TestMissingKeepsBaseline(t *testing.T) {
	data := buildStream(t, func(e *Encoder) {
		e.Encode(3, 50)
		e.EncodeMissing(3)
		e.Encode(3, 60) // delta +10 against the retained baseline
	})
	got, err := decodeAll(data, 64)
	if err != nil {
		t.Fatal(err)
	}
	mustStatus(t, got[0], StatusOK, 50)
	mustStatus(t, got[1], StatusMissing, 0)
	mustStatus(t, got[2], StatusOK, 60)
}

func TestResetDropsBaseline(t *testing.T) {
	data := buildStream(t, func(e *Encoder) {
		e.Encode(9, 1)
		e.Encode(9, 2)
		e.Reset(9)
		e.Encode(9, 100) // encoder must emit a fresh anchor
	})
	got, err := decodeAll(data, 64)
	if err != nil {
		t.Fatal(err)
	}
	mustStatus(t, got[0], StatusOK, 1)
	mustStatus(t, got[1], StatusOK, 2)
	mustStatus(t, got[2], StatusUnknown, 0)
	mustStatus(t, got[3], StatusOK, 100)
}

func TestDeltaWithoutBaseline(t *testing.T) {
	data := rawFrame(TagDelta, 42, encodeZigZag(99))
	got, err := decodeAll(data, 64)
	if err != nil {
		t.Fatalf("unanchored delta must not be a protocol error: %v", err)
	}
	mustStatus(t, got[0], StatusUnknown, 0)

	// A later anchor resolves the channel.
	more := buildStream(t, func(e *Encoder) { e.EncodeAnchor(42, 500) })
	dec := NewDecoder()
	dec.Feed(data)
	dec.Feed(more)
	s1, err := dec.Decode()
	if err != nil {
		t.Fatal(err)
	}
	mustStatus(t, s1, StatusUnknown, 0)
	s2, err := dec.Decode()
	if err != nil {
		t.Fatal(err)
	}
	mustStatus(t, s2, StatusOK, 500)
}

func TestLostDeltaUnknownUntilAnchor(t *testing.T) {
	// Anchor then four deltas on the wire; drop the first delta on purpose to
	// simulate frame loss.
	var buf bytes.Buffer
	enc := NewEncoder(&buf)
	enc.Encode(5, 1000) // anchor
	for _, v := range []int64{1001, 1002, 1003, 1004} {
		enc.Encode(5, v)
	}
	full := buf.Bytes()

	// Split into individual frames and drop the first delta (frame #1).
	frames := splitFrames(full)
	if len(frames) != 5 {
		t.Fatalf("got %d frames, want 5", len(frames))
	}
	stream := bytes.Join([][]byte{frames[0], frames[2], frames[3], frames[4]}, nil)
	got, err := decodeAll(stream, 64)
	if err != nil {
		t.Fatal(err)
	}
	mustStatus(t, got[0], StatusOK, 1000)
	// Baseline is stale from here on: no reconstruction allowed.
	for i := 1; i < len(got); i++ {
		mustStatus(t, got[i], StatusUnknown, 0)
	}

	// Until an anchor arrives; then the channel recovers.
	rec := buildStream(t, func(e *Encoder) { e.EncodeAnchor(5, 2000) })
	dec := NewDecoder()
	dec.Feed(stream)
	dec.Feed(rec)
	var last Sample
	for {
		smp, err := dec.Decode()
		if err == ErrNeedMoreData {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		last = smp
	}
	mustStatus(t, last, StatusOK, 2000)
}

func TestChannelIsolation(t *testing.T) {
	data := buildStream(t, func(e *Encoder) {
		e.Encode(1, 100)
		e.Encode(2, 200)
		e.Reset(1)
	})
	got, err := decodeAll(data, 64)
	if err != nil {
		t.Fatal(err)
	}
	mustStatus(t, got[0], StatusOK, 100)
	mustStatus(t, got[1], StatusOK, 200)
	mustStatus(t, got[2], StatusUnknown, 0)

	// Channel 2 still has a valid baseline; channel 1 does not.
	more := buildStream(t, func(e *Encoder) {
		e.Encode(2, 250)
	})
	dec := NewDecoder()
	dec.Feed(data)
	for {
		if _, err := dec.Decode(); err == ErrNeedMoreData {
			break
		}
	}
	dec.Feed(more)
	smp, err := dec.Decode()
	if err != nil {
		t.Fatal(err)
	}
	mustStatus(t, smp, StatusOK, 250)
}

func TestArbitraryChunkSizes(t *testing.T) {
	data := buildStream(t, func(e *Encoder) {
		for i := int64(0); i < 30; i++ {
			e.Encode(uint32(i%3), i*i-50)
			if i%7 == 0 {
				e.EncodeMissing(uint32(i % 3))
			}
		}
	})
	expected, err := decodeAll(data, len(data))
	if err != nil {
		t.Fatal(err)
	}
	for _, chunk := range []int{1, 2, 3, 5, 7, 64, 1000} {
		got, err := decodeAll(data, chunk)
		if err != nil {
			t.Fatalf("chunk size %d: %v", chunk, err)
		}
		if len(got) != len(expected) {
			t.Fatalf("chunk size %d: got %d samples want %d", chunk, len(got), len(expected))
		}
		for i := range got {
			if got[i] != expected[i] {
				t.Fatalf("chunk size %d sample %d: got %+v want %+v", chunk, i, got[i], expected[i])
			}
		}
	}
}

func TestEmptyFeedNeedMore(t *testing.T) {
	dec := NewDecoder()
	if _, err := dec.Decode(); err != ErrNeedMoreData {
		t.Fatalf("empty buffer: got %v want ErrNeedMoreData", err)
	}
	dec.Feed(rawFrame(TagAnchor, 1, encodeZigZag(5))[:2]) // partial frame
	if _, err := dec.Decode(); err != ErrNeedMoreData {
		t.Fatalf("partial frame: got %v want ErrNeedMoreData", err)
	}
	dec.Feed(rawFrame(TagAnchor, 1, encodeZigZag(5))[2:])
	smp, err := dec.Decode()
	if err != nil {
		t.Fatal(err)
	}
	mustStatus(t, smp, StatusOK, 5)
}

func TestBadCRCIsCorruptAndFatal(t *testing.T) {
	good := rawFrame(TagAnchor, 1, encodeZigZag(5))
	bad := rawFrameCRC(TagAnchor, 2, 0, encodeZigZag(6), false)
	dec := NewDecoder()
	dec.Feed(append(good, bad...))
	if smp, err := dec.Decode(); err != nil {
		t.Fatalf("first good frame: %v", err)
	} else {
		mustStatus(t, smp, StatusOK, 5)
	}
	if _, err := dec.Decode(); err != ErrCorrupt {
		t.Fatalf("bad crc: got %v want ErrCorrupt", err)
	}
	if _, err := dec.Decode(); err != ErrCorrupt {
		t.Fatalf("decoder must stay dead after corruption: got %v", err)
	}
}

func TestUnknownTagIsCorrupt(t *testing.T) {
	dec := NewDecoder()
	dec.Feed([]byte{0x7F, 1, 0, 0, 0, 0, 0})
	if _, err := dec.Decode(); err != ErrCorrupt {
		t.Fatalf("got %v want ErrCorrupt", err)
	}
}

func TestSemanticBadFramesAreCorrupt(t *testing.T) {
	cases := map[string][]byte{
		"missing with payload":  rawFrame(TagMissing, 1, encodeZigZag(1)),
		"reset with payload":    rawFrame(TagReset, 1, encodeZigZag(1)),
		"empty anchor payload":  rawFrame(TagAnchor, 1, nil),
		"anchor trailing bytes": rawFrame(TagAnchor, 1, append(encodeZigZag(1), 0)),
		"non-minimal varint":    {TagAnchor, 0x80, 0x00, 0, 0, 0, 0, 0, 0, 0, 0},
		"anchor seq must be 0":  rawFrameSeq(TagAnchor, 1, 7, encodeZigZag(9)),
	}
	for name, frame := range cases {
		dec := NewDecoder()
		dec.Feed(rawFrame(TagAnchor, 1, encodeZigZag(math.MaxInt64)))
		dec.Feed(frame)
		if _, err := dec.Decode(); err != nil {
			t.Fatalf("%s: setup: %v", name, err)
		}
		if _, err := dec.Decode(); err != ErrCorrupt {
			t.Fatalf("%s: got %v want ErrCorrupt", name, err)
		}
	}

	// Baseline MaxInt64 plus delta 1 wraps: corrupt.
	dec := NewDecoder()
	dec.Feed(rawFrame(TagAnchor, 1, encodeZigZag(math.MaxInt64)))
	dec.Feed(rawFrameSeq(TagDelta, 1, 1, encodeZigZag(1)))
	if _, err := dec.Decode(); err != nil {
		t.Fatal(err)
	}
	if _, err := dec.Decode(); err != ErrCorrupt {
		t.Fatalf("overflow delta: got %v want ErrCorrupt", err)
	}
}

func TestPayloadQuotaCheckedBeforeAllocation(t *testing.T) {
	// Claims a 1 GiB payload; actual stream is short, so it must be rejected
	// from the length field alone without attempting the allocation.
	var claim []byte
	claim = append(claim, TagAnchor, 1)
	claim = appendUvarint(claim, 0) // seq
	claim = appendUvarint(claim, 1<<30)
	dec := NewDecoder()
	dec.Feed(claim)
	if _, err := dec.Decode(); err != ErrCorrupt {
		t.Fatalf("oversized claim: got %v want ErrCorrupt", err)
	}

	// A payload exactly within the custom quota works; one byte over does not.
	decOK := NewDecoder()
	decOK.SetMaxPayload(10)
	decOK.Feed(rawFrame(TagAnchor, 1, encodeZigZag(12345))) // payload <= 10
	smp, err := decOK.Decode()
	if err != nil {
		t.Fatal(err)
	}
	mustStatus(t, smp, StatusOK, 12345)
}

func TestOverflowEmitsAnchor(t *testing.T) {
	data := buildStream(t, func(e *Encoder) {
		e.Encode(1, math.MaxInt64)
		e.Encode(1, math.MinInt64) // cannot be a delta: absolute anchor
		e.Encode(1, 0)             // ordinary delta after recovery
	})
	got, err := decodeAll(data, 64)
	if err != nil {
		t.Fatal(err)
	}
	mustStatus(t, got[0], StatusOK, math.MaxInt64)
	mustStatus(t, got[1], StatusOK, math.MinInt64)
	mustStatus(t, got[2], StatusOK, 0)
	if framesTag(data)[1] != TagAnchor {
		t.Fatal("extreme difference must be encoded as an anchor")
	}
}

func TestPeriodicAnchor(t *testing.T) {
	var buf bytes.Buffer
	enc := NewEncoder(&buf)
	enc.SetAnchorPeriod(3)
	for i := int64(1); i <= 8; i++ {
		enc.Encode(7, i)
	}
	tags := framesTag(buf.Bytes())
	// Frame 1 anchor, deltas 2-4, anchor 5, deltas 6-8, anchor 9 not reached.
	wantTags := []byte{TagAnchor, TagDelta, TagDelta, TagDelta, TagAnchor, TagDelta, TagDelta, TagDelta}
	if len(tags) != len(wantTags) {
		t.Fatalf("got %d frames want %d", len(tags), len(wantTags))
	}
	for i, w := range wantTags {
		if tags[i] != w {
			t.Fatalf("frame %d tag = %#x want %#x", i, tags[i], w)
		}
	}
}

func TestNegativeDeltas(t *testing.T) {
	data := buildStream(t, func(e *Encoder) {
		e.Encode(0, 0)
		e.Encode(0, -1)
		e.Encode(0, math.MinInt64)
		e.Encode(0, math.MaxInt64) // overflow -> anchor
	})
	got, err := decodeAll(data, 1) // single-byte chunks
	if err != nil {
		t.Fatal(err)
	}
	mustStatus(t, got[0], StatusOK, 0)
	mustStatus(t, got[1], StatusOK, -1)
	mustStatus(t, got[2], StatusOK, math.MinInt64)
	mustStatus(t, got[3], StatusOK, math.MaxInt64)
}

func TestStreamDecoder(t *testing.T) {
	data := buildStream(t, func(e *Encoder) {
		e.Encode(1, 10)
		e.Encode(1, 12)
		e.EncodeMissing(1)
	})
	sd := NewStreamDecoder(bytes.NewReader(data))
	var vals []int64
	var statuses []Status
	for {
		smp, err := sd.Decode()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		statuses = append(statuses, smp.Status)
		if smp.Status == StatusOK {
			vals = append(vals, smp.Value)
		}
	}
	if len(vals) != 2 || vals[0] != 10 || vals[1] != 12 {
		t.Fatalf("vals = %v", vals)
	}
	if statuses[2] != StatusMissing {
		t.Fatalf("third sample status = %d", statuses[2])
	}

	// Truncated final frame => unexpected EOF.
	trunc := data[:len(data)-2]
	sd2 := NewStreamDecoder(bytes.NewReader(trunc))
	for {
		_, err := sd2.Decode()
		if err == io.ErrUnexpectedEOF {
			return
		}
		if err != nil && err != ErrNeedMoreData {
			t.Fatalf("got %v", err)
		}
	}
}

// splitFrames slices a byte stream into its self-delimited frames.
func splitFrames(data []byte) [][]byte {
	var frames [][]byte
	rest := data
	for len(rest) > 0 {
		_, n1, err := readUvarint(rest[1:])
		if err != nil {
			panic(err)
		}
		_, n2, err := readUvarint(rest[1+n1:])
		if err != nil {
			panic(err)
		}
		plen, n3, err := readUvarint(rest[1+n1+n2:])
		if err != nil {
			panic(err)
		}
		size := 1 + n1 + n2 + n3 + int(plen) + 4
		frames = append(frames, rest[:size])
		rest = rest[size:]
	}
	return frames
}

func framesTag(data []byte) []byte {
	var tags []byte
	for _, f := range splitFrames(data) {
		tags = append(tags, f[0])
	}
	return tags
}
