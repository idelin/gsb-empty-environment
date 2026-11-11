package tsblock

import (
	"math"
	"math/rand"
	"testing"
)

func TestRejectsOutOfOrder(t *testing.T) {
	enc := NewEncoder(0)
	if err := enc.Append(Sample{T: 10, V: 1}); err != nil {
		t.Fatal(err)
	}
	if err := enc.Append(Sample{T: 10, V: 2}); err != ErrOutOfOrder {
		t.Fatalf("duplicate timestamp: got %v want %v", err, ErrOutOfOrder)
	}
	if err := enc.Append(Sample{T: 9, V: 2}); err != ErrOutOfOrder {
		t.Fatalf("backward timestamp: got %v want %v", err, ErrOutOfOrder)
	}
	if err := enc.Append(Sample{T: 11, V: 2}); err != nil {
		t.Fatalf("in-order append failed: %v", err)
	}
}

func TestEmptyAndSealedErrors(t *testing.T) {
	enc := NewEncoder(0)
	if _, err := enc.Seal(); err != ErrEmpty {
		t.Fatalf("empty seal: got %v want %v", err, ErrEmpty)
	}

	if err := enc.Append(Sample{T: 1, V: 1}); err != nil {
		t.Fatal(err)
	}
	if _, err := enc.Seal(); err != nil {
		t.Fatal(err)
	}
	if err := enc.Append(Sample{T: 2, V: 1}); err != ErrBlockSealed {
		t.Fatalf("append after seal: got %v want %v", err, ErrBlockSealed)
	}
	if _, err := enc.Seal(); err != ErrBlockSealed {
		t.Fatalf("double seal: got %v want %v", err, ErrBlockSealed)
	}
}

func TestSingleSampleFrame(t *testing.T) {
	s := Sample{T: 123456789, V: math.Copysign(0, -1)}
	frames, got := encodeAll(t, []Sample{s}, DefaultMaxBlockSize)
	if len(frames) != 1 || len(got) != 1 {
		t.Fatalf("frames=%d samples=%d", len(frames), len(got))
	}
	assertRoundTrip(t, []Sample{s}, got)
}

func TestBlockSealingBySize(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	samples := make([]Sample, 5000)
	ts := uint64(1000)
	for i := range samples {
		ts += 100
		samples[i] = Sample{T: ts, V: rng.Float64()}
	}
	const maxSize = 512
	frames, got := encodeAll(t, samples, maxSize)
	assertRoundTrip(t, samples, got)

	for i, f := range frames {
		if len(f) > maxSize+64 { // tolerance for the sample that crosses the cap
			t.Fatalf("frame %d size %d exceeds cap %d", i, len(f), maxSize)
		}
	}
}

func TestCorruptFrameRejected(t *testing.T) {
	samples := []Sample{{T: 1, V: 1}, {T: 2, V: 2}, {T: 3, V: 3}}
	frames, _ := encodeAll(t, samples, DefaultMaxBlockSize)
	frame := append([]byte(nil), frames[0]...)

	// Truncating the body must surface an error rather than panic.
	if _, err := NewDecoder(frame[:len(frame)-1]); err != ErrCorrupt {
		t.Fatalf("truncated frame: got %v want %v", err, ErrCorrupt)
	}

	bad := append([]byte(nil), frame...)
	bad[0] = 'X'
	if _, err := NewDecoder(bad); err != ErrCorrupt {
		t.Fatalf("bad magic: got %v want %v", err, ErrCorrupt)
	}
}

func TestSeekByTime(t *testing.T) {
	rng := rand.New(rand.NewSource(99))
	samples := realisticSamples(6000, rng)
	frames, _ := encodeAll(t, samples, 800)

	targetIdx := 4200
	target := samples[targetIdx]

	var dec *Decoder
	for _, frame := range frames {
		d, err := NewDecoder(frame)
		if err != nil {
			t.Fatal(err)
		}
		if d.FirstTime() <= target.T {
			dec = d
		}
	}
	if dec == nil {
		t.Fatal("no frame contains target")
	}

	if !dec.Seek(target.T) {
		t.Fatalf("seek to %d failed", target.T)
	}
	if dec.Time() != target.T || math.Float64bits(dec.Value()) != math.Float64bits(target.V) {
		t.Fatalf("seek landed on (%d, %#x), want (%d, %#x)",
			dec.Time(), math.Float64bits(dec.Value()), target.T, math.Float64bits(target.V))
	}

	// Seeking between timestamps lands on the next sample.
	want := samples[targetIdx+1]
	if !dec.Seek(target.T + 1) {
		t.Fatal("seek to gap failed")
	}
	if dec.Time() != want.T || math.Float64bits(dec.Value()) != math.Float64bits(want.V) {
		t.Fatalf("gap seek landed on %d want %d", dec.Time(), want.T)
	}

	// Seek before the first sample returns the frame start.
	if !dec.Seek(0) || dec.Index() != 0 || dec.Time() != dec.FirstTime() {
		t.Fatal("seek to 0 did not return first sample")
	}

	// Seek past the end reports false and leaves the decoder on the last sample.
	lastT := dec.firstT
	for dec.Next() {
		lastT = dec.Time()
	}
	if dec.Seek(lastT + 1) {
		t.Fatal("seek past last sample unexpectedly succeeded")
	}
	if dec.Time() != lastT {
		t.Fatalf("after failed seek decoder at %d, want %d", dec.Time(), lastT)
	}

	// After a mid-frame Seek, scanning forward reproduces the remaining tail
	// bit-for-bit, proving value state stays synchronized during timestamp seek.
	if !dec.Seek(target.T) {
		t.Fatal("re-seek failed")
	}
	landedFrameIndex := dec.Index()
	frameCount := dec.Count()
	var tail []Sample
	for dec.Next() {
		tail = append(tail, Sample{T: dec.Time(), V: dec.Value()})
	}
	if err := dec.Err(); err != nil {
		t.Fatal(err)
	}

	// Locate the landed sample in the global slice by its unique timestamp.
	landedGlobal := 0
	for landedGlobal = range samples {
		if samples[landedGlobal].T == target.T {
			break
		}
	}
	// Seek is scoped to a single frame: the expected tail ends at this frame's
	// last sample, not at the end of the whole sample stream.
	// Remaining samples including the landed one is count-index.
	frameEnd := landedGlobal + int(frameCount-landedFrameIndex)
	wantTail := samples[landedGlobal:frameEnd]
	assertRoundTrip(t, wantTail, tail)
}
