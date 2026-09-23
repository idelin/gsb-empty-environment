package rangecoder

import (
	"bytes"
	"math/rand"
	"testing"
)

func testData(rng *rand.Rand, n int) []byte {
	p := make([]byte, n)
	for i := range p {
		// skewed distribution so the adaptive model actually adapts
		p[i] = byte(rng.Intn(8))
	}
	return p
}

func TestRoundTrip(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	for _, n := range []int{0, 1, 7, 100, 5000} {
		data := testData(rng, n)
		enc := NewEncoder()
		enc.Encode(data)
		stream := enc.FinishSegment()

		dec := NewDecoder(stream)
		got, err := dec.ReadAll()
		if err != nil {
			t.Fatalf("n=%d: %v", n, err)
		}
		if !bytes.Equal(got, data) {
			t.Fatalf("n=%d: round trip mismatch", n)
		}
	}
}

// Resuming from a checkpoint must produce a bitstream identical to a
// continuous run following the same segmenting policy.
func TestResumeBitstreamIdentical(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	a := testData(rng, 300)
	b := testData(rng, 300)
	c := testData(rng, 300)

	// Continuous run: three segments, snapshots at boundaries.
	enc := NewEncoder()
	enc.Encode(a)
	seg1 := enc.FinishSegment()
	cp1 := enc.Snapshot()
	enc.Encode(b)
	seg2 := enc.FinishSegment()
	cp2 := enc.Snapshot()
	enc.Encode(c)
	seg3 := enc.FinishSegment()

	// Resumed run: replay only the suffixes from checkpoints.
	encB, err := NewEncoderFromCheckpoint(cp1)
	if err != nil {
		t.Fatal(err)
	}
	encB.Encode(b)
	if got := encB.FinishSegment(); !bytes.Equal(got, seg2) {
		t.Fatal("resumed segment 2 differs from continuous run")
	}

	encC, err := NewEncoderFromCheckpoint(cp2)
	if err != nil {
		t.Fatal(err)
	}
	encC.Encode(c)
	if got := encC.FinishSegment(); !bytes.Equal(got, seg3) {
		t.Fatal("resumed segment 3 differs from continuous run")
	}

	// Sanity: segments are independent streams of plausible size.
	if len(seg1) == 0 || len(seg2) == 0 || len(seg3) == 0 {
		t.Fatal("empty segment stream")
	}
}

// A decoder starting from a checkpoint decodes a later segment without
// ever touching earlier segment data.
func TestRandomAccessDecode(t *testing.T) {
	rng := rand.New(rand.NewSource(11))
	parts := [][]byte{testData(rng, 200), testData(rng, 200), testData(rng, 200)}

	enc := NewEncoder()
	var streams, cps [][]byte
	for i, p := range parts {
		enc.Encode(p)
		streams = append(streams, enc.FinishSegment())
		if i+1 < len(parts) {
			cps = append(cps, enc.Snapshot())
		}
	}

	for i := 1; i < len(parts); i++ {
		dec, err := NewDecoderFromCheckpoint(cps[i-1], streams[i])
		if err != nil {
			t.Fatal(err)
		}
		got, err := dec.ReadAll()
		if err != nil {
			t.Fatalf("segment %d: %v", i, err)
		}
		if !bytes.Equal(got, parts[i]) {
			t.Fatalf("segment %d: random-access decode mismatch", i)
		}
	}
}

// Corruption in an earlier segment must not pollute the model used for
// an independent later segment.
func TestCorruptionIsolation(t *testing.T) {
	rng := rand.New(rand.NewSource(23))
	a := testData(rng, 500)
	b := testData(rng, 500)

	enc := NewEncoder()
	enc.Encode(a)
	seg1 := enc.FinishSegment()
	cp := enc.Snapshot()
	enc.Encode(b)
	seg2 := enc.FinishSegment()

	// Trash segment 1 thoroughly.
	for i := range seg1 {
		seg1[i] ^= 0xFF
	}

	dec, err := NewDecoderFromCheckpoint(cp, seg2)
	if err != nil {
		t.Fatal(err)
	}
	got, err := dec.ReadAll()
	if err != nil {
		t.Fatalf("later segment polluted by earlier corruption: %v", err)
	}
	if !bytes.Equal(got, b) {
		t.Fatal("later segment decoded incorrectly after earlier corruption")
	}
}

// Decoding output is bounded by the quota, for valid and garbage streams.
func TestDecodeQuotaBounded(t *testing.T) {
	data := bytes.Repeat([]byte("abcabcabd"), 200) // 1800 bytes
	enc := NewEncoder()
	enc.Encode(data)
	stream := enc.FinishSegment()

	dec := NewDecoder(stream)
	dec.SetQuota(100)
	if _, err := dec.ReadAll(); err != ErrQuotaExceeded {
		t.Fatalf("want ErrQuotaExceeded, got %v", err)
	}

	// Adversarial stream (all zero bytes decodes symbol 0 forever):
	// must stop exactly at the quota, never hang or overflow it.
	dec2 := NewDecoder(make([]byte, 64))
	dec2.SetQuota(1000)
	out, err := dec2.ReadAll()
	if err != ErrQuotaExceeded {
		t.Fatalf("want ErrQuotaExceeded, got %v", err)
	}
	if len(out) > 1000 {
		t.Fatalf("quota not enforced: %d bytes", len(out))
	}

	// Random garbage: must terminate within the quota one way or another.
	garbage := make([]byte, 64)
	rng := rand.New(rand.NewSource(1))
	rng.Read(garbage)
	dec3 := NewDecoder(garbage)
	dec3.SetQuota(1000)
	out3, _ := dec3.ReadAll()
	if len(out3) > 1000 {
		t.Fatalf("quota not enforced on garbage: %d bytes", len(out3))
	}
}

// A truncated or corrupted stream must terminate (error or wrong output)
// within the quota: it can never decode back to the original data.
func TestTruncatedStreamBounded(t *testing.T) {
	data := []byte("hello world, hello range coder, hello checkpoint")
	enc := NewEncoder()
	enc.Encode(data)
	stream := enc.FinishSegment()
	stream = stream[:2] // truncate hard so decoding runs off the end

	dec := NewDecoder(stream)
	dec.SetQuota(10000)
	got, err := dec.ReadAll()
	if err == nil && bytes.Equal(got, data) {
		t.Fatal("truncated stream decoded back to original data")
	}
	if len(got) > 10000 {
		t.Fatalf("quota not enforced: %d bytes", len(got))
	}
}

// Sequential decoding carries the model across segment boundaries via
// NextSegment, mirroring the encoder.
func TestSequentialSegmentDecode(t *testing.T) {
	rng := rand.New(rand.NewSource(5))
	parts := [][]byte{testData(rng, 100), testData(rng, 100), testData(rng, 100)}

	enc := NewEncoder()
	var streams [][]byte
	for _, p := range parts {
		enc.Encode(p)
		streams = append(streams, enc.FinishSegment())
	}

	dec := NewDecoder(streams[0])
	for i, p := range parts {
		if i > 0 {
			dec.NextSegment(streams[i])
		}
		got, err := dec.ReadAll()
		if err != nil {
			t.Fatalf("segment %d: %v", i, err)
		}
		if !bytes.Equal(got, p) {
			t.Fatalf("segment %d: sequential decode mismatch", i)
		}
	}
}
