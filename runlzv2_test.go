package runlzv2

import (
	"bytes"
	"io"
	"testing"
)

// bruteForceCost is an independent O(n^2) DP used to verify planOptimal.
func bruteForceCost(src []byte) int64 {
	n := len(src)
	const inf = int64(1) << 62
	dp := make([]int64, n+1)
	for j := 1; j <= n; j++ {
		dp[j] = inf
	}
	for j := 1; j <= n; j++ {
		for i := j - 1; i >= 0 && j-i <= MaxBlockLen; i-- {
			if c := dp[i] + BlockHeaderLen + int64(j-i); c < dp[j] {
				dp[j] = c
			}
			if j-i >= 2 {
				same := true
				for k := i + 1; k < j; k++ {
					if src[k] != src[i] {
						same = false
						break
					}
				}
				if same {
					if c := dp[i] + BlockHeaderLen; c < dp[j] {
						dp[j] = c
					}
				}
			}
		}
	}
	return dp[n]
}

func segmentsCost(segs []segment) int64 {
	var total int64
	for _, s := range segs {
		if s.kind == kindLiteral {
			total += int64(BlockHeaderLen + s.end - s.start)
		} else {
			total += BlockHeaderLen
		}
	}
	return total
}

func checkSegments(t *testing.T, src []byte, segs []segment) {
	t.Helper()
	pos := 0
	for _, s := range segs {
		if s.start != pos || s.end <= s.start || s.end > len(src) {
			t.Fatalf("bad segment %+v at pos %d", s, pos)
		}
		if s.end-s.start > MaxBlockLen {
			t.Fatalf("segment too long: %+v", s)
		}
		if s.kind == kindRepeat {
			if s.end-s.start < 2 {
				t.Fatalf("repeat block too short: %+v", s)
			}
			for k := s.start + 1; k < s.end; k++ {
				if src[k] != src[s.start] {
					t.Fatalf("repeat block spans different bytes: %+v", s)
				}
			}
		}
		pos = s.end
	}
	if pos != len(src) {
		t.Fatalf("segments end at %d, want %d", pos, len(src))
	}
}

func enumerate(alphabet []byte, maxLen int, fn func([]byte)) {
	var cur []byte
	var rec func()
	rec = func() {
		fn(append([]byte(nil), cur...))
		if len(cur) == maxLen {
			return
		}
		for _, b := range alphabet {
			cur = append(cur, b)
			rec()
			cur = cur[:len(cur)-1]
		}
	}
	rec()
}

func TestPlanOptimalExhaustive(t *testing.T) {
	enumerate([]byte{'a', 'b'}, 14, func(src []byte) {
		segs := planOptimal(src)
		checkSegments(t, src, segs)
		if got, want := segmentsCost(segs), bruteForceCost(src); got != want {
			t.Fatalf("src=%q cost=%d want %d", src, got, want)
		}
	})
	enumerate([]byte{'x', 'y', 'z'}, 10, func(src []byte) {
		segs := planOptimal(src)
		checkSegments(t, src, segs)
		if got, want := segmentsCost(segs), bruteForceCost(src); got != want {
			t.Fatalf("src=%q cost=%d want %d", src, got, want)
		}
	})
}

func TestShortRepeatDoesNotSwallowLiteral(t *testing.T) {
	// "aab": repeat("aa")+literal("b") = 4+5 = 9 > literal("aab") = 7.
	segs := planOptimal([]byte("aab"))
	if len(segs) != 1 || segs[0].kind != kindLiteral {
		t.Fatalf("expected single literal block, got %+v", segs)
	}
	// "aaaaab": repeat x5 (4) + literal b (5) = 9 < literal x6 (10).
	segs = planOptimal([]byte("aaaaab"))
	if len(segs) != 2 || segs[0].kind != kindRepeat || segs[1].kind != kindLiteral {
		t.Fatalf("expected repeat+literal, got %+v", segs)
	}
	if segs[1].start != 5 || segs[1].end != 6 {
		t.Fatalf("repeat swallowed adjacent literal: %+v", segs)
	}
}

func roundTrip(t *testing.T, src []byte) []byte {
	t.Helper()
	enc, err := Encode(src)
	if err != nil {
		t.Fatal(err)
	}
	out, err := Decode(enc, int64(len(src)))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(out, src) {
		t.Fatalf("roundtrip mismatch: in=%q out=%q", src, out)
	}
	return enc
}

func TestRoundTripExhaustiveSmall(t *testing.T) {
	enumerate([]byte{'a', 'b'}, 12, func(src []byte) {
		if len(src) == 0 {
			return
		}
		roundTrip(t, src)
	})
}

func TestWorstCaseExpansionBound(t *testing.T) {
	// Pseudo-random (incompressible) data: literal blocks only.
	src := make([]byte, 200000)
	x := uint32(12345)
	for i := range src {
		x = x*1664525 + 1013904223
		src[i] = byte(x >> 24)
	}
	enc, err := Encode(src)
	if err != nil {
		t.Fatal(err)
	}
	blocks := int64(len(src)+MaxBlockLen-1) / MaxBlockLen
	bound := int64(FrameHeaderLen) + int64(len(src)) + blocks*BlockHeaderLen
	if int64(len(enc)) > bound {
		t.Fatalf("encoded %d exceeds worst-case bound %d", len(enc), bound)
	}
	roundTrip(t, src)
}

func TestEmptyInputDistinctions(t *testing.T) {
	enc, err := Encode(nil)
	if err != nil || enc != nil {
		t.Fatalf("offline empty: got %v, %v", enc, err)
	}
	out, err := Decode(nil, 0)
	if err != nil || len(out) != 0 {
		t.Fatalf("decode empty: got %v, %v", out, err)
	}

	// Explicit empty flush frame is distinguishable from no frame.
	var buf bytes.Buffer
	e := NewEncoder(&buf)
	if err := e.Flush(); err != nil {
		t.Fatal(err)
	}
	if buf.Len() != FrameHeaderLen {
		t.Fatalf("empty flush frame = %d bytes, want %d", buf.Len(), FrameHeaderLen)
	}
	if err := e.Close(); err != nil {
		t.Fatal(err)
	}
	out, err = Decode(buf.Bytes(), 0)
	if err != nil || len(out) != 0 {
		t.Fatalf("decode flush+final: got %v, %v", out, err)
	}
}

func TestConsecutiveFlushes(t *testing.T) {
	var buf bytes.Buffer
	e := NewEncoder(&buf)
	parts := [][]byte{[]byte("aa"), []byte("bbbbb"), []byte("c")}
	for _, p := range parts {
		if _, err := e.Write(p); err != nil {
			t.Fatal(err)
		}
		if err := e.Flush(); err != nil {
			t.Fatal(err)
		}
	}
	// Two consecutive flushes with no data between them.
	if err := e.Flush(); err != nil {
		t.Fatal(err)
	}
	if err := e.Flush(); err != nil {
		t.Fatal(err)
	}
	if err := e.Close(); err != nil {
		t.Fatal(err)
	}

	r := NewReader(bytes.NewReader(buf.Bytes()))
	var out []byte
	tmp := make([]byte, 3)
	boundaries := 0
	for {
		n, err := r.Read(tmp)
		if n > 0 {
			out = append(out, tmp[:n]...)
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if n == 0 {
			boundaries++
		}
	}
	if boundaries != 5 {
		t.Fatalf("got %d flush boundaries, want 5", boundaries)
	}
	if string(out) != "aabbbbbc" {
		t.Fatalf("got %q", out)
	}
}

func TestFlushIsNotStreamEnd(t *testing.T) {
	var buf bytes.Buffer
	e := NewEncoder(&buf)
	if _, err := e.Write([]byte("xy")); err != nil {
		t.Fatal(err)
	}
	if err := e.Flush(); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Write([]byte("zz")); err != nil {
		t.Fatal(err)
	}
	if err := e.Close(); err != nil {
		t.Fatal(err)
	}
	out, err := Decode(buf.Bytes(), 16)
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != "xyzz" {
		t.Fatalf("got %q", out)
	}
}

func TestRepeatedShortBlocks(t *testing.T) {
	// Alternating short repeats and literals across many small flushes.
	var buf bytes.Buffer
	e, err := NewEncoderBuffer(&buf, 4)
	if err != nil {
		t.Fatal(err)
	}
	var want []byte
	for i := 0; i < 50; i++ {
		chunk := []byte{'a' + byte(i%3), 'a' + byte(i%3), byte('A' + i%26)}
		want = append(want, chunk...)
		if _, err := e.Write(chunk); err != nil {
			t.Fatal(err)
		}
		if err := e.Flush(); err != nil {
			t.Fatal(err)
		}
	}
	if err := e.Close(); err != nil {
		t.Fatal(err)
	}
	out, err := Decode(buf.Bytes(), int64(len(want)))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(out, want) {
		t.Fatal("repeated short blocks mismatch")
	}
}

func TestBackpressureNoDuplicateBytes(t *testing.T) {
	src := []byte("the quick brown fox jumps over the lazy dog the quick brown fox")
	enc := roundTrip(t, src)

	// Decode one byte at a time (extreme backpressure).
	r := NewReader(bytes.NewReader(enc))
	var out []byte
	one := make([]byte, 1)
	for {
		n, err := r.Read(one)
		if n > 0 {
			out = append(out, one[:n]...)
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	if !bytes.Equal(out, src) {
		t.Fatalf("backpressure decode mismatch: %q", out)
	}
}

func TestOutputLimit(t *testing.T) {
	src := bytes.Repeat([]byte("z"), 1000)
	enc, err := Encode(src)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Decode(enc, 999); err != ErrOutputLimit {
		t.Fatalf("want ErrOutputLimit, got %v", err)
	}
	out, err := Decode(enc, 1000)
	if err != nil || !bytes.Equal(out, src) {
		t.Fatalf("limit=1000: %v", err)
	}
}

func TestCorruptionDetection(t *testing.T) {
	src := []byte("some payload some payload")
	enc, err := Encode(src)
	if err != nil {
		t.Fatal(err)
	}

	// Bit flip in payload -> checksum error.
	bad := append([]byte(nil), enc...)
	bad[len(bad)-1] ^= 0xff
	if _, err := Decode(bad, 1<<20); err != ErrChecksum {
		t.Fatalf("want ErrChecksum, got %v", err)
	}

	// Bad version.
	bad = append([]byte(nil), enc...)
	bad[0] = 99
	if _, err := Decode(bad, 1<<20); err != ErrFormat {
		t.Fatalf("want ErrFormat, got %v", err)
	}

	// Truncated.
	if _, err := Decode(enc[:len(enc)-3], 1<<20); err == nil {
		t.Fatal("want error for truncated stream")
	}
}

func TestOnlineGapBound(t *testing.T) {
	src := make([]byte, 5000)
	x := uint32(999)
	for i := range src {
		x = x*1103515245 + 12345
		if i%7 == 0 {
			src[i] = 'q' // create runs
		} else {
			src[i] = byte(x >> 16)
		}
	}
	offline, err := Encode(src)
	if err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	e, err := NewEncoderBuffer(&buf, 256)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.Write(src); err != nil {
		t.Fatal(err)
	}
	if err := e.Close(); err != nil {
		t.Fatal(err)
	}
	online := buf.Bytes()

	out, err := Decode(online, int64(len(src)))
	if err != nil || !bytes.Equal(out, src) {
		t.Fatalf("online roundtrip failed: %v", err)
	}

	frames := (len(src) + 255) / 256
	boundaries := frames - 1
	gapBound := boundaries * (FrameHeaderLen + BlockHeaderLen)
	if gap := len(online) - len(offline); gap > gapBound {
		t.Fatalf("online gap %d exceeds bound %d", gap, gapBound)
	}
	t.Logf("offline=%d online=%d gap=%d bound=%d",
		len(offline), len(online), len(online)-len(offline), gapBound)
}

func TestEncoderWriteAfterClose(t *testing.T) {
	var buf bytes.Buffer
	e := NewEncoder(&buf)
	if err := e.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Write([]byte("x")); err != ErrClosed {
		t.Fatalf("want ErrClosed, got %v", err)
	}
	if err := e.Flush(); err != ErrClosed {
		t.Fatalf("want ErrClosed, got %v", err)
	}
}
