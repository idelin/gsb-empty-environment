package rlecomp

import (
	"bytes"
	"encoding/binary"
	"errors"
	"math"
	"math/rand/v2"
	"testing"
)

// bruteForceOptimal enumerates every composition of src and takes the
// cheapest encoding of each part (literal, or repeat if uniform). It is
// the ground truth for the DP splitter on short inputs.
func bruteForceOptimal(src []byte) int64 {
	n := len(src)
	best := int64(math.MaxInt64)
	var rec func(i int, cost int64)
	rec = func(i int, cost int64) {
		if cost >= best {
			return
		}
		if i == n {
			best = cost
			return
		}
		for j := i + 1; j <= n; j++ {
			l := j - i
			c := int64(headerLen(l) + l)
			uniform := true
			for k := i + 1; k < j; k++ {
				if src[k] != src[i] {
					uniform = false
					break
				}
			}
			if uniform {
				if rc := int64(headerLen(l) + 1); rc < c {
					c = rc
				}
			}
			rec(j, cost+c)
		}
	}
	if n == 0 {
		return 0
	}
	rec(0, 0)
	return best
}

// framePayloads parses a stream and returns the raw payloads of all frames.
func framePayloads(t *testing.T, stream []byte) [][]byte {
	t.Helper()
	var out [][]byte
	for len(stream) > 0 {
		if len(stream) < 3 || stream[0] != magic[0] || stream[1] != magic[1] {
			t.Fatalf("bad frame header")
		}
		v, n := binary.Uvarint(stream[3:])
		if n <= 0 {
			t.Fatalf("bad payload length")
		}
		start := 3 + n
		end := start + int(v)
		if end+4 > len(stream) {
			t.Fatalf("truncated frame")
		}
		out = append(out, stream[start:end])
		stream = stream[end+4:]
	}
	return out
}

func TestHeaderBound(t *testing.T) {
	for _, n := range []int{1, 2, 127, 128, 16383, 16384, MaxBlockLen} {
		if got := headerLen(n); got > MaxHeaderLen {
			t.Fatalf("headerLen(%d) = %d > MaxHeaderLen", n, got)
		}
	}
	if got := headerLen(MaxBlockLen); got != MaxHeaderLen {
		t.Fatalf("headerLen(MaxBlockLen) = %d, want exactly MaxHeaderLen = %d", got, MaxHeaderLen)
	}
}

// TestOptimalExhaustive checks, for every binary string up to length 10,
// that the DP splitter matches brute-force enumeration and that the
// offline encoder emits exactly that many payload bytes.
func TestOptimalExhaustive(t *testing.T) {
	for n := 0; n <= 10; n++ {
		for bits := 0; bits < 1<<n; bits++ {
			src := make([]byte, n)
			for i := range src {
				if bits&(1<<i) != 0 {
					src[i] = 'b'
				} else {
					src[i] = 'a'
				}
			}
			want := bruteForceOptimal(src)
			if got := optimalCost(src); got != want {
				t.Fatalf("optimalCost(%q) = %d, brute force = %d", src, got, want)
			}
			stream := Compress(src)
			payloads := framePayloads(t, stream)
			if len(payloads) != 1 {
				t.Fatalf("Compress(%q): %d frames, want 1", src, len(payloads))
			}
			if int64(len(payloads[0])) != want {
				t.Fatalf("Compress(%q) payload = %d bytes, optimum = %d", src, len(payloads[0]), want)
			}
			dec, frames, err := DecodeAll(bytes.NewReader(stream), 1<<20)
			if err != nil || frames != 1 || !bytes.Equal(dec, src) {
				t.Fatalf("roundtrip(%q): dec=%q frames=%d err=%v", src, dec, frames, err)
			}
		}
	}
}

// TestOptimalRandomTernary spot-checks the DP against brute force on
// random 3-symbol strings, which mix runs and literals.
func TestOptimalRandomTernary(t *testing.T) {
	rng := rand.New(rand.NewPCG(1, 2))
	for i := 0; i < 300; i++ {
		n := 1 + rng.IntN(12)
		src := make([]byte, n)
		for j := range src {
			src[j] = byte('a' + rng.IntN(3))
		}
		if got, want := optimalCost(src), bruteForceOptimal(src); got != want {
			t.Fatalf("optimalCost(%q) = %d, brute force = %d", src, got, want)
		}
	}
}

// TestShortRepeatBoundaries makes sure short repeats never swallow
// adjacent literal bytes.
func TestShortRepeatBoundaries(t *testing.T) {
	cases := []string{
		"a", "ab", "aa", "aab", "abb", "aba", "aabb", "aaab", "abbb",
		"aaabaa", "xyzzzwk", "aaaabaaaac", "ababaaaab", "aaaa",
		"aaaaaaaaaaaaaaaaaaaab", "baaaaaaaaaaaaaaaaaaaa",
	}
	for _, s := range cases {
		stream := Compress([]byte(s))
		dec, _, err := DecodeAll(bytes.NewReader(stream), 1<<20)
		if err != nil || string(dec) != s {
			t.Fatalf("roundtrip(%q) = %q, err=%v", s, dec, err)
		}
	}
}

func TestEmptyFrameVsNoFrame(t *testing.T) {
	// No frame: zero-byte stream decodes to zero frames, no error.
	dec, frames, err := DecodeAll(bytes.NewReader(nil), 0)
	if err != nil || frames != 0 || len(dec) != 0 {
		t.Fatalf("empty stream: dec=%q frames=%d err=%v", dec, frames, err)
	}
	// Empty frame: closing an encoder without input emits one empty frame.
	var buf bytes.Buffer
	if err := NewEncoder(&buf, 16).Close(); err != nil {
		t.Fatal(err)
	}
	if buf.Len() == 0 {
		t.Fatal("empty input must still produce a frame")
	}
	dec, frames, err = DecodeAll(bytes.NewReader(buf.Bytes()), 0)
	if err != nil || frames != 1 || len(dec) != 0 {
		t.Fatalf("empty frame: dec=%q frames=%d err=%v", dec, frames, err)
	}
	// Offline Compress of empty input agrees.
	if got := len(Compress(nil)); got == 0 {
		t.Fatal("Compress(nil) must emit an empty frame")
	}
}

func TestFlushIsNotEndOfStream(t *testing.T) {
	var buf bytes.Buffer
	enc := NewEncoder(&buf, 8)
	if _, err := enc.Write([]byte("hello")); err != nil {
		t.Fatal(err)
	}
	if err := enc.Flush(); err != nil {
		t.Fatal(err)
	}
	// Stream continues after flush.
	if _, err := enc.Write([]byte("world")); err != nil {
		t.Fatal(err)
	}
	if err := enc.Flush(); err != nil {
		t.Fatal(err)
	}
	if err := enc.Close(); err != nil {
		t.Fatal(err)
	}
	dec, frames, err := DecodeAll(bytes.NewReader(buf.Bytes()), 1<<20)
	if err != nil || string(dec) != "helloworld" {
		t.Fatalf("dec=%q err=%v", dec, err)
	}
	if frames != 2 {
		t.Fatalf("frames = %d, want 2 (no trailing empty frame after data)", frames)
	}
}

func TestConsecutiveFlush(t *testing.T) {
	var buf bytes.Buffer
	enc := NewEncoder(&buf, 4)
	for i := 0; i < 5; i++ {
		if err := enc.Flush(); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := enc.Write([]byte("x")); err != nil {
		t.Fatal(err)
	}
	if err := enc.Close(); err != nil {
		t.Fatal(err)
	}
	dec, frames, err := DecodeAll(bytes.NewReader(buf.Bytes()), 1<<20)
	if err != nil || string(dec) != "x" {
		t.Fatalf("dec=%q err=%v", dec, err)
	}
	// One empty frame from the first flush, one frame for "x".
	if frames != 2 {
		t.Fatalf("frames = %d, want 2", frames)
	}
}

// flakyWriter fails the first failWrites calls, then accepts at most
// maxChunk bytes per call (short writes without error).
type flakyWriter struct {
	buf        bytes.Buffer
	maxChunk   int
	failWrites int
}

var errWouldBlock = errors.New("would block")

func (f *flakyWriter) Write(p []byte) (int, error) {
	if f.failWrites > 0 {
		f.failWrites--
		return 0, errWouldBlock
	}
	n := min(len(p), f.maxChunk)
	f.buf.Write(p[:n])
	return n, nil
}

// TestBackpressureResume verifies that a blocking writer never causes
// lost or duplicated output bytes.
func TestBackpressureResume(t *testing.T) {
	rng := rand.New(rand.NewPCG(3, 4))
	var input []byte
	for len(input) < 3000 {
		if rng.IntN(2) == 0 {
			input = append(input, bytes.Repeat([]byte{byte('A' + rng.IntN(4))}, 1+rng.IntN(40))...)
		} else {
			input = append(input, byte('a'+rng.IntN(26)))
		}
	}
	w := &flakyWriter{maxChunk: 7, failWrites: 11}
	enc := NewEncoder(w, 32)
	for off := 0; off < len(input); {
		n := min(1+rng.IntN(100), len(input)-off)
		if _, err := enc.Write(input[off : off+n]); err != nil {
			for enc.Resume() != nil {
			}
		}
		off += n
		if rng.IntN(3) == 0 {
			for enc.Flush() != nil {
			}
		}
	}
	for enc.Close() != nil {
	}
	dec, _, err := DecodeAll(bytes.NewReader(w.buf.Bytes()), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(dec, input) {
		t.Fatalf("recovered stream mismatch: got %d bytes, want %d", len(dec), len(input))
	}
}

func TestRepeatedShortBlocks(t *testing.T) {
	var buf bytes.Buffer
	enc := NewEncoder(&buf, 3)
	var want []byte
	for i := 0; i < 200; i++ {
		chunk := []byte("aabbcc")
		want = append(want, chunk...)
		if _, err := enc.Write(chunk); err != nil {
			t.Fatal(err)
		}
	}
	if err := enc.Close(); err != nil {
		t.Fatal(err)
	}
	dec, _, err := DecodeAll(bytes.NewReader(buf.Bytes()), 1<<20)
	if err != nil || !bytes.Equal(dec, want) {
		t.Fatalf("mismatch: got %d bytes, want %d, err=%v", len(dec), len(want), err)
	}
}

func TestQuota(t *testing.T) {
	src := []byte("hello hello hello hello")
	stream := Compress(src)
	if _, _, err := DecodeAll(bytes.NewReader(stream), int64(len(src))); err != nil {
		t.Fatalf("exact quota: %v", err)
	}
	if _, _, err := DecodeAll(bytes.NewReader(stream), int64(len(src)-1)); !errors.Is(err, ErrQuotaExceeded) {
		t.Fatalf("short quota: %v", err)
	}
	// A hostile repeat block must hit the quota, not allocate output.
	payload := append([]byte{tagRepeat}, binary.AppendUvarint(nil, MaxBlockLen)...)
	payload = append(payload, 'x')
	var frame []byte
	frame = appendFrame(frame, payload)
	if _, _, err := DecodeAll(bytes.NewReader(frame), 100); !errors.Is(err, ErrQuotaExceeded) {
		t.Fatalf("hostile repeat: %v", err)
	}
}

func TestCorruptStreams(t *testing.T) {
	src := []byte("some data with aaaaaaaaa run")
	good := Compress(src)

	bad := bytes.Clone(good)
	bad[len(bad)-1] ^= 0xff // crc
	if _, _, err := DecodeAll(bytes.NewReader(bad), 1<<20); !errors.Is(err, ErrBadChecksum) {
		t.Fatalf("crc: %v", err)
	}
	bad = bytes.Clone(good)
	bad[2] = 99 // version
	if _, _, err := DecodeAll(bytes.NewReader(bad), 1<<20); !errors.Is(err, ErrBadVersion) {
		t.Fatalf("version: %v", err)
	}
	bad = bytes.Clone(good)
	bad[0] = 'X' // magic
	if _, _, err := DecodeAll(bytes.NewReader(bad), 1<<20); !errors.Is(err, ErrBadMagic) {
		t.Fatalf("magic: %v", err)
	}
	if _, _, err := DecodeAll(bytes.NewReader(good[:len(good)-3]), 1<<20); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("truncated: %v", err)
	}
	// Block length beyond MaxBlockLen must be rejected.
	payload := append([]byte{tagLiteral}, binary.AppendUvarint(nil, MaxBlockLen+1)...)
	var frame []byte
	frame = appendFrame(frame, payload)
	if _, _, err := DecodeAll(bytes.NewReader(frame), 1<<30); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("oversize block: %v", err)
	}
}

// TestWorstCaseExpansion: incompressible input expands by at most
// MaxHeaderLen + frameOverhead bytes offline.
func TestWorstCaseExpansion(t *testing.T) {
	rng := rand.New(rand.NewPCG(5, 6))
	src := make([]byte, 100000)
	for i := range src {
		src[i] = byte(rng.IntN(256))
	}
	got := len(Compress(src))
	if limit := len(src) + MaxHeaderLen + frameOverhead; got > limit {
		t.Fatalf("expansion %d > %d", got, limit)
	}
}

// TestOnlineGap verifies the documented bound: online output exceeds the
// offline optimum by at most (MaxHeaderLen+1) per forced boundary.
func TestOnlineGap(t *testing.T) {
	rng := rand.New(rand.NewPCG(7, 8))
	var src []byte
	for len(src) < 20000 {
		src = append(src, bytes.Repeat([]byte{byte('a' + rng.IntN(8))}, 1+rng.IntN(200))...)
	}
	const window = 128
	var buf bytes.Buffer
	enc := NewEncoder(&buf, window)
	flushes := 0
	for off := 0; off < len(src); {
		n := min(1+rng.IntN(500), len(src)-off)
		if _, err := enc.Write(src[off : off+n]); err != nil {
			t.Fatal(err)
		}
		off += n
		if rng.IntN(4) == 0 {
			if err := enc.Flush(); err != nil {
				t.Fatal(err)
			}
			flushes++
		}
	}
	if err := enc.Close(); err != nil {
		t.Fatal(err)
	}
	dec, _, err := DecodeAll(bytes.NewReader(buf.Bytes()), 1<<30)
	if err != nil || !bytes.Equal(dec, src) {
		t.Fatalf("online roundtrip failed: err=%v", err)
	}
	var onlinePayload int
	for _, p := range framePayloads(t, buf.Bytes()) {
		onlinePayload += len(p)
	}
	boundaries := (len(src)+window-1)/window + flushes
	limit := optimalCost(src) + int64(MaxHeaderLen+1)*int64(boundaries)
	if int64(onlinePayload) > limit {
		t.Fatalf("online payload %d exceeds bound %d", onlinePayload, limit)
	}
	t.Logf("offline optimum %d, online payload %d, bound %d", optimalCost(src), onlinePayload, limit)
}

// TestStreamingMatchesOffline: streaming with a large window and a single
// final flush must produce exactly the offline-optimal payload.
func TestStreamingMatchesOffline(t *testing.T) {
	rng := rand.New(rand.NewPCG(9, 10))
	var src []byte
	for len(src) < 5000 {
		src = append(src, bytes.Repeat([]byte{byte(rng.IntN(4))}, 1+rng.IntN(30))...)
	}
	var buf bytes.Buffer
	enc := NewEncoder(&buf, 1<<20)
	if _, err := enc.Write(src); err != nil {
		t.Fatal(err)
	}
	if err := enc.Close(); err != nil {
		t.Fatal(err)
	}
	payloads := framePayloads(t, buf.Bytes())
	if int64(len(payloads[0])) != optimalCost(src) {
		t.Fatalf("streaming payload %d != offline optimum %d", len(payloads[0]), optimalCost(src))
	}
}
