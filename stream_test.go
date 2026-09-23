package rangecoder

import (
	"bytes"
	"errors"
	"math/rand"
	"testing"
)

// encodeChunks applies the checkpoint policy shared by the continuous and
// the resumed encoding paths: encode data in seg-sized chunks, checkpoint
// after every chunk.
func encodeChunks(e *Encoder, data []byte, seg int) {
	for off := 0; off < len(data); off += seg {
		end := off + seg
		if end > len(data) {
			end = len(data)
		}
		e.Encode(data[off:end])
		e.Checkpoint()
	}
}

func encodeWithCheckpoints(data []byte, seg int) ([]byte, []Checkpoint) {
	e := NewEncoder()
	encodeChunks(e, data, seg)
	return e.Finish(), e.Checkpoints()
}

// encodeContinuous encodes without any checkpoints: a single segment.
func encodeContinuous(data []byte) []byte {
	e := NewEncoder()
	e.Encode(data)
	return e.Finish()
}

// textLike generates compressible text-ish data deterministically.
func textLike(n int, seed int64) []byte {
	r := rand.New(rand.NewSource(seed))
	words := []string{"the ", "quick ", "brown ", "fox ", "jumps ", "over ",
		"the ", "lazy ", "dog. ", "range ", "coding ", "with ", "checkpoints "}
	var b bytes.Buffer
	for b.Len() < n {
		b.WriteString(words[r.Intn(len(words))])
	}
	return b.Bytes()[:n]
}

func TestRoundTripText(t *testing.T) {
	data := textLike(20000, 1)
	stream, cps := encodeWithCheckpoints(data, 4096)
	got, err := DecodeSegments(stream, cps, uint64(len(data))+1)
	if err != nil {
		t.Fatalf("DecodeSegments: %v", err)
	}
	if !bytes.Equal(got, data) {
		t.Fatalf("round trip mismatch: got %d bytes, want %d", len(got), len(data))
	}
	t.Logf("text: %d bytes -> %d bytes (%.1f%%)", len(data), len(stream),
		100*float64(len(stream))/float64(len(data)))
}

func TestRoundTripBinaryAndEmpty(t *testing.T) {
	r := rand.New(rand.NewSource(2))
	cases := map[string][]byte{
		"empty":    {},
		"one byte": {0x42},
		"all bytes": func() []byte {
			b := make([]byte, 256*4)
			for i := range b {
				b[i] = byte(i)
			}
			return b
		}(),
		"random": func() []byte { b := make([]byte, 5000); r.Read(b); return b }(),
		"zeros":  make([]byte, 3000),
	}
	for name, data := range cases {
		t.Run(name, func(t *testing.T) {
			stream, cps := encodeWithCheckpoints(data, 1024)
			got, err := DecodeSegments(stream, cps, uint64(len(data))+16)
			if err != nil {
				t.Fatalf("DecodeSegments: %v", err)
			}
			if !bytes.Equal(got, data) {
				t.Fatalf("round trip mismatch: got %d bytes, want %d", len(got), len(data))
			}
		})
	}
}

// A stream encoded without checkpoints decodes in one continuous pass.
func TestRoundTripContinuous(t *testing.T) {
	data := textLike(15000, 42)
	stream := encodeContinuous(data)
	got, err := DecodeAll(stream, uint64(len(data))+1)
	if err != nil {
		t.Fatalf("DecodeAll: %v", err)
	}
	if !bytes.Equal(got, data) {
		t.Fatalf("continuous round trip mismatch: got %d, want %d", len(got), len(data))
	}
}

// A resumed run must produce a bitstream identical to a continuous run with
// the same checkpoint policy.
func TestResumeBitstreamIdentical(t *testing.T) {
	data := textLike(30000, 3)
	const seg = 1000
	full, cps := encodeWithCheckpoints(data, seg)
	if len(cps) == 0 {
		t.Fatal("no checkpoints recorded")
	}
	for k := range cps {
		cp := cps[k]
		e, err := ResumeEncoder(full, cp)
		if err != nil {
			t.Fatalf("ResumeEncoder(%d): %v", k, err)
		}
		encodeChunks(e, data[cp.SymbolIndex:], seg)
		got := e.Finish()
		if !bytes.Equal(got, full) {
			t.Fatalf("resume from checkpoint %d: stream differs (got %d bytes, want %d)",
				k, len(got), len(full))
		}
	}
}

// Resumed model state must equal the continuous model state exactly.
func TestResumeModelStateEqual(t *testing.T) {
	data := textLike(5000, 4)
	full, cps := encodeWithCheckpoints(data, 700)
	for k, cp := range cps {
		e, err := ResumeEncoder(full, cp)
		if err != nil {
			t.Fatalf("ResumeEncoder(%d): %v", k, err)
		}
		if !e.m.Equal(cp.Model) {
			t.Fatalf("checkpoint %d: resumed model differs from snapshot", k)
		}
		if e.syms != cp.SymbolIndex {
			t.Fatalf("checkpoint %d: symbol count %d, want %d", k, e.syms, cp.SymbolIndex)
		}
	}
}

// Every segment must be decodable on its own, starting from the checkpoint
// that precedes it.
func TestRandomAccessDecode(t *testing.T) {
	data := textLike(20000, 5)
	const seg = 1500
	stream, cps := encodeWithCheckpoints(data, seg)

	// Segment 0 starts at the implicit initial checkpoint.
	d0, err := NewDecoder(stream, Checkpoint{Offset: StreamHeaderLen, Model: NewModel()})
	if err != nil {
		t.Fatal(err)
	}
	got0, err := d0.Decode(uint64(cps[0].SymbolIndex))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got0, data[:cps[0].SymbolIndex]) {
		t.Fatal("segment 0 mismatch")
	}

	// Later segments start at their preceding checkpoint, in random order.
	order := rand.New(rand.NewSource(6)).Perm(len(cps))
	for _, i := range order {
		cp := cps[i]
		end := uint64(len(data))
		if i+1 < len(cps) {
			end = cps[i+1].SymbolIndex
		}
		n := end - cp.SymbolIndex
		d, err := NewDecoder(stream, cp)
		if err != nil {
			t.Fatalf("segment %d: %v", i+1, err)
		}
		got, err := d.Decode(n)
		if err != nil {
			t.Fatalf("segment %d: %v", i+1, err)
		}
		if !bytes.Equal(got, data[cp.SymbolIndex:end]) {
			t.Fatalf("segment %d mismatch (%d bytes)", i+1, n)
		}
	}
}

// Corruption in an earlier segment must not pollute the decoder state of
// independent later segments.
func TestCorruptionIsolation(t *testing.T) {
	data := textLike(12000, 7)
	const seg = 1000
	stream, cps := encodeWithCheckpoints(data, seg)
	if len(cps) < 3 {
		t.Fatalf("want at least 3 checkpoints, got %d", len(cps))
	}

	bad := append([]byte(nil), stream...)
	// Corrupt a byte well inside segment 0's code range.
	pos := StreamHeaderLen + 10
	if pos >= int(cps[0].Offset) {
		t.Fatal("segment 0 too small for test")
	}
	bad[pos] ^= 0xFF

	// Later segments still decode perfectly from their own checkpoints.
	for i := 0; i+1 < len(cps); i++ {
		cp := cps[i]
		end := cps[i+1].SymbolIndex
		d, err := NewDecoder(bad, cp)
		if err != nil {
			t.Fatalf("checkpoint %d: %v", i, err)
		}
		got, err := d.Decode(end - cp.SymbolIndex)
		if err != nil {
			t.Fatalf("segment %d after corruption: %v", i+1, err)
		}
		if !bytes.Equal(got, data[cp.SymbolIndex:end]) {
			t.Fatalf("segment %d polluted by earlier corruption", i+1)
		}
	}
}

// The decoder output quota is a hard bound, and decoding stops at the
// end-of-stream marker even with a huge quota.
func TestDecodeQuotaBounded(t *testing.T) {
	data := textLike(8000, 8)
	stream := encodeContinuous(data)

	const quota = 100
	got, err := DecodeAll(stream, quota)
	if err != nil {
		t.Fatal(err)
	}
	if uint64(len(got)) != quota {
		t.Fatalf("quota: got %d bytes, want exactly %d", len(got), quota)
	}
	if !bytes.Equal(got, data[:quota]) {
		t.Fatal("quota-limited prefix mismatch")
	}

	full, err := DecodeAll(stream, 1<<40)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(full, data) {
		t.Fatalf("EOF stop: got %d bytes, want %d", len(full), len(data))
	}

	// The quota also bounds segmented decoding in total.
	cstream, cps := encodeWithCheckpoints(data, 1000)
	segGot, err := DecodeSegments(cstream, cps, quota)
	if err != nil {
		t.Fatal(err)
	}
	if uint64(len(segGot)) != quota || !bytes.Equal(segGot, data[:quota]) {
		t.Fatalf("segmented quota: got %d bytes", len(segGot))
	}
}

func TestStreamVersionRejected(t *testing.T) {
	stream, _ := encodeWithCheckpoints([]byte("hello"), 64)
	bad := append([]byte(nil), stream...)
	bad[5] = 99 // version field
	if _, err := DecodeAll(bad, 100); !errors.Is(err, ErrUnsupportedVersion) {
		t.Fatalf("got %v, want ErrUnsupportedVersion", err)
	}
}

func TestResumeRejectsBadOffset(t *testing.T) {
	stream, cps := encodeWithCheckpoints(textLike(3000, 9), 500)
	cp := cps[0]
	cp.Offset = uint64(len(stream)) + 100
	if _, err := ResumeEncoder(stream, cp); !errors.Is(err, ErrBadOffset) {
		t.Fatalf("got %v, want ErrBadOffset", err)
	}
	if _, err := NewDecoder(stream, cp); !errors.Is(err, ErrBadOffset) {
		t.Fatalf("got %v, want ErrBadOffset", err)
	}
}
