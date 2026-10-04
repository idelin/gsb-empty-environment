package pcf_test

import (
	"bytes"
	"math/rand"
	"reflect"
	"strings"
	"testing"

	"pcf"
)

// buildStream encodes each payload as a frame (seq = index) and
// concatenates them, returning the stream and each frame's offset.
func buildStream(datas ...[]byte) ([]byte, []int) {
	var stream []byte
	var offs []int
	for i, d := range datas {
		offs = append(offs, len(stream))
		stream = append(stream, pcf.EncodeFrame(uint32(i), d)...)
	}
	return stream, offs
}

func decodeWhole(stream []byte) []pcf.Event {
	r := pcf.NewReader(pcf.Options{})
	evs := r.Feed(stream)
	evs = append(evs, r.Close()...)
	return evs
}

// checkCoverage verifies that events tile [0,total) exactly once: in
// order, contiguous, non-overlapping. This is what guarantees that no
// byte is ever read twice or dropped.
func checkCoverage(t *testing.T, evs []pcf.Event, total int) {
	t.Helper()
	pos := int64(0)
	for i, e := range evs {
		if e.Offset != pos {
			t.Fatalf("event %d at offset %d, want %d (overlap or hole)", i, e.Offset, pos)
		}
		if e.Kind != pcf.EventFrame && len(e.Data) != e.Length {
			t.Fatalf("event %d (%s): Data length %d != Length %d", i, e.Kind, len(e.Data), e.Length)
		}
		pos += int64(e.Length)
	}
	if pos != int64(total) {
		t.Fatalf("events cover %d bytes, stream is %d bytes", pos, total)
	}
}

func frameSeqs(evs []pcf.Event) []uint32 {
	var seqs []uint32
	for _, e := range evs {
		if e.Kind == pcf.EventFrame {
			seqs = append(seqs, e.Seq)
		}
	}
	return seqs
}

func badRanges(evs []pcf.Event) []pcf.Event {
	var out []pcf.Event
	for _, e := range evs {
		if e.Kind == pcf.EventBad {
			out = append(out, e)
		}
	}
	return out
}

// coveredByBad reports whether every byte of [start,end) lies inside a
// Bad event.
func coveredByBad(evs []pcf.Event, start, end int) bool {
	for off := start; off < end; off++ {
		inside := false
		for _, e := range evs {
			if e.Kind == pcf.EventBad && int64(off) >= e.Offset && int64(off) < e.Offset+int64(e.Length) {
				inside = true
				break
			}
		}
		if !inside {
			return false
		}
	}
	return true
}

func randomBytes(n int, seed int64) []byte {
	rng := rand.New(rand.NewSource(seed))
	b := make([]byte, n)
	rng.Read(b)
	return b
}

func samplePayloads() [][]byte {
	all := make([]byte, 0, 256)
	for i := 0; i < 256; i++ {
		all = append(all, byte(i))
	}
	return [][]byte{
		{},
		{0x00},
		[]byte("hello world, hello world, hello prefix codes"),
		[]byte(strings.Repeat("z", 500)),
		randomBytes(3000, 42),
		bytes.Repeat(all, 4),
	}
}

func TestRoundTrip(t *testing.T) {
	datas := samplePayloads()
	stream, _ := buildStream(datas...)
	evs := decodeWhole(stream)
	checkCoverage(t, evs, len(stream))
	var got [][]byte
	for _, e := range evs {
		if e.Kind != pcf.EventFrame {
			t.Fatalf("unexpected %s event at offset %d (len %d, reason %q)", e.Kind, e.Offset, e.Length, e.Reason)
		}
		got = append(got, e.Data)
	}
	if !reflect.DeepEqual(got, datas) {
		t.Fatalf("decoded payloads differ from originals")
	}
}

func TestChunkedFeeding(t *testing.T) {
	datas := samplePayloads()
	stream, _ := buildStream(datas...)
	want := decodeWhole(stream)
	for _, size := range []int{1, 3, 7, 64, 1024} {
		r := pcf.NewReader(pcf.Options{})
		var evs []pcf.Event
		for off := 0; off < len(stream); off += size {
			end := off + size
			if end > len(stream) {
				end = len(stream)
			}
			evs = append(evs, r.Feed(stream[off:end])...)
		}
		evs = append(evs, r.Close()...)
		if !reflect.DeepEqual(evs, want) {
			t.Fatalf("chunk size %d: events differ from whole-stream decode", size)
		}
		checkCoverage(t, evs, len(stream))
	}
}

func TestMagicInsidePayloadIsNotAFrameHeader(t *testing.T) {
	// Payload stuffed with magic bytes and header-like garbage.
	fake := append([]byte("prefix"), pcf.Magic...)
	fake = append(fake, bytes.Repeat([]byte{0xFF}, 300)...)
	fake = append(fake, pcf.Magic...)
	fake = append(fake, []byte("suffix")...)
	datas := [][]byte{[]byte("first"), fake, []byte("last")}
	stream, _ := buildStream(datas...)
	evs := decodeWhole(stream)
	checkCoverage(t, evs, len(stream))
	if seqs := frameSeqs(evs); !reflect.DeepEqual(seqs, []uint32{0, 1, 2}) {
		t.Fatalf("got seqs %v, want [0 1 2]", seqs)
	}
	if !bytes.Equal(evs[1].Data, fake) {
		t.Fatalf("frame 1 payload corrupted")
	}
}

func TestInsertedFakeMagicBetweenFrames(t *testing.T) {
	datas := [][]byte{randomBytes(200, 1), randomBytes(200, 2), randomBytes(200, 3)}
	stream, offs := buildStream(datas...)
	filler := bytes.Repeat([]byte{0xA5}, 50)
	corrupt := pcf.InsertFakeMagic(stream, offs[1], filler...)
	insertLen := len(pcf.Magic) + len(filler)
	evs := decodeWhole(corrupt)
	checkCoverage(t, evs, len(corrupt))
	if seqs := frameSeqs(evs); !reflect.DeepEqual(seqs, []uint32{0, 1, 2}) {
		t.Fatalf("got seqs %v, want [0 1 2]", seqs)
	}
	bad := badRanges(evs)
	if len(bad) != 1 {
		t.Fatalf("got %d bad ranges, want exactly 1", len(bad))
	}
	if bad[0].Offset != int64(offs[1]) || bad[0].Length != insertLen {
		t.Fatalf("bad range = [%d,%d), want [%d,%d)",
			bad[0].Offset, bad[0].Offset+int64(bad[0].Length), offs[1], offs[1]+insertLen)
	}
}

func TestInsertedTruncatedHead(t *testing.T) {
	datas := [][]byte{
		randomBytes(300, 10), randomBytes(300, 11), randomBytes(300, 12),
		randomBytes(300, 13), randomBytes(300, 14),
	}
	stream, offs := buildStream(datas...)
	const headLen = 150
	corrupt := pcf.InsertTruncatedHead(stream, offs[2], 99, []byte("tiny"), headLen)
	evs := decodeWhole(corrupt)
	checkCoverage(t, evs, len(corrupt))
	seqs := frameSeqs(evs)
	// Frames before the corruption and frames after the damaged region
	// must survive; frame 2 is swallowed by the misaligned consumption.
	for _, want := range []uint32{0, 1, 3, 4} {
		found := false
		for _, s := range seqs {
			if s == want {
				found = true
			}
		}
		if !found {
			t.Fatalf("seq %d missing from decoded frames %v", want, seqs)
		}
	}
	for _, s := range seqs {
		if s == 99 {
			t.Fatalf("fake truncated head was decoded as a frame")
		}
	}
	// Every actually-inserted byte must be inside a reported bad range.
	if !coveredByBad(evs, offs[2], offs[2]+headLen) {
		t.Fatalf("bad ranges do not cover the inserted truncated head")
	}
	// Each surviving frame must be reported exactly once.
	seen := map[uint32]int{}
	for _, s := range seqs {
		seen[s]++
		if seen[s] > 1 {
			t.Fatalf("seq %d decoded more than once", s)
		}
	}
}

func TestCorruptCodeTable(t *testing.T) {
	datas := [][]byte{randomBytes(200, 20), randomBytes(200, 21), randomBytes(200, 22), randomBytes(200, 23)}
	stream, offs := buildStream(datas...)
	corrupt := pcf.CorruptCodeTable(stream, offs[1])
	evs := decodeWhole(corrupt)
	checkCoverage(t, evs, len(corrupt))
	if seqs := frameSeqs(evs); !reflect.DeepEqual(seqs, []uint32{0, 2, 3}) {
		t.Fatalf("got seqs %v, want [0 2 3]", seqs)
	}
	bad := badRanges(evs)
	if len(bad) != 1 || bad[0].Offset != int64(offs[1]) || bad[0].Length != offs[2]-offs[1] {
		t.Fatalf("bad ranges %+v, want exactly frame 1's range [%d,%d)", bad, offs[1], offs[2])
	}
}

func TestSingleBitFlipInPayload(t *testing.T) {
	datas := [][]byte{randomBytes(200, 30), randomBytes(500, 31), randomBytes(200, 32)}
	stream, offs := buildStream(datas...)
	// Flip one bit in the middle of frame 1's payload.
	corrupt := pcf.FlipBit(stream, offs[1]+pcf.HeaderSize+100, 3)
	evs := decodeWhole(corrupt)
	checkCoverage(t, evs, len(corrupt))
	if seqs := frameSeqs(evs); !reflect.DeepEqual(seqs, []uint32{0, 2}) {
		t.Fatalf("got seqs %v, want [0 2]", seqs)
	}
	// The header was structurally valid, so the reader consumes exactly
	// the declared frame length: the bad range is precisely frame 1.
	bad := badRanges(evs)
	if len(bad) != 1 || bad[0].Offset != int64(offs[1]) || bad[0].Length != offs[2]-offs[1] {
		t.Fatalf("bad ranges %+v, want exactly frame 1's range [%d,%d)", bad, offs[1], offs[2])
	}
	if !strings.Contains(bad[0].Reason, "CRC") {
		t.Fatalf("bad reason %q, want it to mention CRC", bad[0].Reason)
	}
}

func TestBitFlipInLengthField(t *testing.T) {
	datas := [][]byte{randomBytes(200, 40), randomBytes(200, 41), randomBytes(200, 42)}
	stream, offs := buildStream(datas...)
	// Flip the top bit of PayloadLen's most significant byte: the length
	// becomes astronomically large and the header is rejected.
	corrupt := pcf.FlipBit(stream, offs[1]+pcf.PayloadLenOff+3, 7)
	evs := decodeWhole(corrupt)
	checkCoverage(t, evs, len(corrupt))
	if seqs := frameSeqs(evs); !reflect.DeepEqual(seqs, []uint32{0, 2}) {
		t.Fatalf("got seqs %v, want [0 2]", seqs)
	}
	if !coveredByBad(evs, offs[1], offs[2]) {
		t.Fatalf("bad ranges do not cover corrupted frame 1")
	}
}

func TestScanBudgetPausesAndResumes(t *testing.T) {
	datas := [][]byte{randomBytes(150, 50), randomBytes(150, 51)}
	stream, offs := buildStream(datas...)
	garbage := bytes.Repeat([]byte{0xA5}, 5000)
	full := append(append([]byte{}, stream[:offs[1]]...), garbage...)
	full = append(full, stream[offs[1]:]...)

	r := pcf.NewReader(pcf.Options{ScanBudget: 100})
	var evs []pcf.Event
	evs = append(evs, r.Feed(full)...)
	calls := 1
	for r.Buffered() > 0 {
		evs = append(evs, r.Feed(nil)...)
		calls++
		if calls > 10000 {
			t.Fatalf("reader made no progress under budget")
		}
	}
	evs = append(evs, r.Close()...)
	if calls < 2 {
		t.Fatalf("budget never paused the scan")
	}
	checkCoverage(t, evs, len(full))
	if seqs := frameSeqs(evs); !reflect.DeepEqual(seqs, []uint32{0, 1}) {
		t.Fatalf("got seqs %v, want [0 1]", seqs)
	}
	bad := badRanges(evs)
	if len(bad) != 1 || bad[0].Offset != int64(offs[1]) || bad[0].Length != len(garbage) {
		t.Fatalf("bad ranges %+v, want the 5000-byte garbage region", bad)
	}
	// The second frame survived the pause intact.
	var last *pcf.Event
	for i := range evs {
		if evs[i].Kind == pcf.EventFrame && evs[i].Seq == 1 {
			last = &evs[i]
		}
	}
	if last == nil || !bytes.Equal(last.Data, datas[1]) {
		t.Fatalf("frame after garbage lost or corrupted")
	}
}

func TestCloseWithTruncatedFrameTail(t *testing.T) {
	datas := [][]byte{randomBytes(200, 60), randomBytes(200, 61)}
	stream, offs := buildStream(datas...)
	truncated := stream[:offs[1]+100]
	r := pcf.NewReader(pcf.Options{})
	evs := r.Feed(truncated)
	if seqs := frameSeqs(evs); !reflect.DeepEqual(seqs, []uint32{0}) {
		t.Fatalf("before Close: got seqs %v, want [0]", seqs)
	}
	evs = append(evs, r.Close()...)
	checkCoverage(t, evs, len(truncated))
	last := evs[len(evs)-1]
	if last.Kind != pcf.EventGap {
		t.Fatalf("last event is %s, want gap for the undetermined tail", last.Kind)
	}
	if last.Offset != int64(offs[1]) || last.Length != 100 {
		t.Fatalf("gap = [%d,%d), want [%d,%d)", last.Offset, last.Offset+int64(last.Length), offs[1], offs[1]+100)
	}
	if !bytes.Equal(last.Data, stream[offs[1]:offs[1]+100]) {
		t.Fatalf("gap does not preserve the raw truncated bytes")
	}
}

func TestTrailingGarbageAtClose(t *testing.T) {
	stream, _ := buildStream(randomBytes(100, 70))
	garbage := []byte("garbage with no magic at all")
	full := append(stream, garbage...)
	r := pcf.NewReader(pcf.Options{})
	evs := r.Feed(full)
	if seqs := frameSeqs(evs); !reflect.DeepEqual(seqs, []uint32{0}) {
		t.Fatalf("got seqs %v, want [0]", seqs)
	}
	evs = append(evs, r.Close()...)
	checkCoverage(t, evs, len(full))
	last := evs[len(evs)-1]
	if last.Kind != pcf.EventBad || last.Length != len(garbage) {
		t.Fatalf("last event = %s len %d, want bad len %d", last.Kind, last.Length, len(garbage))
	}
}

func TestCompleteFramesEmittedWithoutClose(t *testing.T) {
	stream, _ := buildStream([]byte("aaa"), []byte("bbb"))
	r := pcf.NewReader(pcf.Options{})
	evs := r.Feed(stream)
	if seqs := frameSeqs(evs); !reflect.DeepEqual(seqs, []uint32{0, 1}) {
		t.Fatalf("got seqs %v, want [0 1]", seqs)
	}
	if r.Buffered() != 0 {
		t.Fatalf("reader still buffers %d bytes", r.Buffered())
	}
	if evs := r.Close(); len(evs) != 0 {
		t.Fatalf("Close emitted %d events, want none", len(evs))
	}
}

func TestEmptyStream(t *testing.T) {
	r := pcf.NewReader(pcf.Options{})
	if evs := r.Close(); len(evs) != 0 {
		t.Fatalf("got %d events for empty stream", len(evs))
	}
}
