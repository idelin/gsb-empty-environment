package segcrc

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"strings"
	"testing"
)

func summarize(b []byte) Summary {
	h := NewHasher()
	_, _ = h.Write(b)
	return h.Summary()
}

// feedChunked hashes b using the supplied chunk sizes; zero-sized chunks are
// allowed and the pattern may be irregular.
func feedChunked(b []byte, chunks []int) Summary {
	h := NewHasher()
	pos := 0
	for pos < len(b) {
		for _, c := range chunks {
			end := pos + c
			if end > len(b) {
				end = len(b)
			}
			n, err := h.Write(b[pos:end])
			if err != nil {
				return Summary{}
			}
			if n != end-pos {
				return Summary{}
			}
			pos = end
			if pos == len(b) {
				break
			}
		}
		if len(chunks) == 0 {
			break
		}
	}
	// A few zero-byte writes must leave the digest untouched.
	_, _ = h.Write(nil)
	_, _ = h.Write([]byte{})
	return h.Summary()
}

func TestChunkedHasher(t *testing.T) {
	data := make([]byte, 4096)
	rng := rand.New(rand.NewSource(1))
	_, _ = rng.Read(data)
	want := summarize(data)

	patterns := [][]int{
		{1}, {2}, {3}, {7}, {64}, {1000}, {4096},
		{1, 1, 1}, {3, 0, 5}, {64, 1, 63}, {13, 257, 9, 800},
	}
	for _, p := range patterns {
		if got := feedChunked(data, p); got != want {
			t.Fatalf("chunks %v: got %s want %s", p, got.Encode(), want.Encode())
		}
	}
}

func TestCombineArbitrarySplits(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	for iter := 0; iter < 200; iter++ {
		n := rng.Intn(300) // length zero in some iterations
		data := make([]byte, n)
		_, _ = rng.Read(data)
		want := summarize(data)

		// Pick 0..4 cut points and a random ordering of segments.
		cuts := map[int]bool{0: true, n: true}
		for k := rng.Intn(4); k > 0; k-- {
			cuts[rng.Intn(n+1)] = true
		}
		var cs []int
		for c := range cuts {
			cs = append(cs, c)
		}
		sortInts(cs)

		var parts []Summary
		for i := 0; i+1 < len(cs); i++ {
			parts = append(parts, summarize(data[cs[i]:cs[i+1]]))
		}
		// Sprinkle explicit empty segments; they must be identities.
		parts = append([]Summary{Empty}, parts...)
		parts = append(parts, Empty)

		got, err := Compose(parts...)
		if err != nil {
			t.Fatalf("iter %d: %v", iter, err)
		}
		if got != want {
			t.Fatalf("iter %d len %d splits %v: composed %s != direct %s",
				iter, n, cs, got.Encode(), want.Encode())
		}
	}
}

func sortInts(a []int) {
	for i := 1; i < len(a); i++ {
		for j := i; j > 0 && a[j-1] > a[j]; j-- {
			a[j-1], a[j] = a[j], a[j-1]
		}
	}
}

func TestEmptyIdentity(t *testing.T) {
	a := summarize([]byte("hello"))
	if got, err := Combine(Empty, Empty); err != nil || got != Empty {
		t.Fatalf("empty||empty = %v, %v", got, err)
	}
	if got, err := Combine(Empty, a); err != nil || got != a {
		t.Fatalf("empty||a = %v, %v", got, err)
	}
	if got, err := Combine(a, Empty); err != nil || got != a {
		t.Fatalf("a||empty = %v, %v", got, err)
	}
}

func TestParseSummary(t *testing.T) {
	s := Summary{CRC: 0xcbf5315b, Length: 11}
	got, err := ParseSummary(s.Encode())
	if err != nil || got != s {
		t.Fatalf("round trip: %v %v", got, err)
	}
	if got, err := ParseSummary("00000000:0"); err != nil || got != Empty {
		t.Fatalf("empty parse: %v %v", got, err)
	}
	bad := []string{"", "abcd", "00000000", "zzzzzzzz:1", "0000000:1",
		"00000001:0", "00000000:-1", "00000000:abc"}
	for _, b := range bad {
		if _, err := ParseSummary(b); !errors.Is(err, ErrBadSummary) {
			t.Fatalf("ParseSummary(%q) err = %v, want ErrBadSummary", b, err)
		}
	}
}

type memSource struct {
	data   map[string][]byte
	opens  map[string]int
	deny   map[string]bool // true => data missing
	panics map[string]bool // true => must never be opened
}

func (m *memSource) Open(name string) (io.Reader, bool) {
	if m.opens != nil {
		m.opens[name]++
	}
	if m.panics[name] {
		panic("unchanged segment " + name + " was rescanned")
	}
	if m.deny[name] {
		return nil, false
	}
	b, ok := m.data[name]
	if !ok {
		return nil, false
	}
	return bytes.NewReader(b), true
}

func TestReadSegmentMissingCorruptAndShort(t *testing.T) {
	good := []byte("payload-bytes")
	s := summarize(good)
	src := &memSource{
		data:  map[string][]byte{"good": good, "short": good[:5]},
		deny:  map[string]bool{"gone": true},
		opens: map[string]int{},
	}

	if got, err := ReadSegment(src, "good", s, 1<<20); err != nil || got != s {
		t.Fatalf("good segment: %v %v", got, err)
	}
	if _, err := ReadSegment(src, "gone", s, 1<<20); !errors.Is(err, ErrMissingData) {
		t.Fatalf("missing: %v", err)
	}
	bad := append([]byte(nil), good...)
	bad[0] ^= 0xFF
	src.data["bad"] = bad
	if _, err := ReadSegment(src, "bad", s, 1<<20); !errors.Is(err, ErrCorruptData) {
		t.Fatalf("corrupt: %v", err)
	}
	if _, err := ReadSegment(src, "short", s, 1<<20); !errors.Is(err, ErrCorruptData) {
		t.Fatalf("truncated present bytes must be corrupt not missing: %v", err)
	}
}

func TestQuotaCheckedBeforeAlloc(t *testing.T) {
	src := &memSource{deny: map[string]bool{"big": true}}
	s := Summary{CRC: 0xdeadbeef, Length: 1 << 30}
	// Quota exceeded is reported before touching the source, and it is not
	// mislabeled as missing or corrupt.
	_, err := ReadSegment(src, "big", s, 1024)
	if !errors.Is(err, ErrLenOverflow) {
		t.Fatalf("quota: %v", err)
	}
	if src.opens["big"] != 0 {
		t.Fatalf("source opened despite quota failure")
	}
	// Zero-length with non-zero crc is a bad summary, not a missing read.
	if _, err := ReadSegment(src, "big", Summary{CRC: 1, Length: 0}, 1024); !errors.Is(err, ErrBadSummary) {
		t.Fatalf("bad empty summary: %v", err)
	}
}

func TestLengthOverflow(t *testing.T) {
	big := Summary{CRC: 1, Length: MaxLen}
	if _, err := Combine(big, Summary{CRC: 2, Length: 1}); !errors.Is(err, ErrLenOverflow) {
		t.Fatalf("combine overflow: %v", err)
	}
	h := NewHasher()
	h.n = MaxLen - 2
	if _, err := h.Write(make([]byte, 3)); !errors.Is(err, ErrLenOverflow) {
		t.Fatalf("hasher overflow: %v", err)
	}
	if _, err := Combine(Summary{CRC: 1, Length: -1}, Empty); !errors.Is(err, ErrNegativeLen) {
		t.Fatal("negative length must error")
	}
}

func TestSameDigestDifferentLengthCannotMix(t *testing.T) {
	a := summarize([]byte("AAAA"))
	b := summarize([]byte("BBBB"))
	j, err := NewJoiner("a", "b")
	if err != nil {
		t.Fatal(err)
	}
	if err := j.Set("a", a); err != nil {
		t.Fatal(err)
	}
	if err := j.Set("b", b); err != nil {
		t.Fatal(err)
	}
	got, err := j.Build()
	if err != nil {
		t.Fatal(err)
	}
	// Claim the same CRC but a different total length: verification must
	// refuse to treat the summaries as interchangeable.
	fake := Summary{CRC: got.CRC, Length: got.Length + 1}
	if err := j.Verify(fake); !errors.Is(err, ErrLenMismatch) {
		t.Fatalf("same crc, wrong length: %v", err)
	}
	if err := j.Verify(got); err != nil {
		t.Fatalf("exact summary should verify: %v", err)
	}

	// Unknown name and duplicate name are rejected at the joiner boundary.
	if _, err := NewJoiner("x", "x"); err == nil {
		t.Fatal("duplicate names should fail")
	}
	if err := j.Set("nope", Empty); err == nil {
		t.Fatal("unknown segment name should fail")
	}
}

func TestMissingSegmentStopsBuild(t *testing.T) {
	j, _ := NewJoiner("a", "b")
	_ = j.Set("a", summarize([]byte("hi")))
	if _, err := j.Build(); !errors.Is(err, ErrMissingData) {
		t.Fatalf("build: %v", err)
	}
	if len(j.Missing()) != 1 || j.Missing()[0] != "b" {
		t.Fatalf("missing list: %v", j.Missing())
	}
}

func TestSubsegmentReplacementNoRescan(t *testing.T) {
	// Logical stream: head | middle | tail, stored separately.
	head := []byte(strings.Repeat("HEAD-", 100))
	mid := []byte("the middle we will replace")
	tail := []byte(strings.Repeat("-TAIL", 100))
	whole := func(h, m, t []byte) []byte {
		out := make([]byte, 0, len(h)+len(m)+len(t))
		out = append(out, h...)
		out = append(out, m...)
		out = append(out, t...)
		return out
	}

	src := &memSource{
		data:   map[string][]byte{"head": head, "mid": mid, "tail": tail},
		opens:  map[string]int{},
		panics: map[string]bool{},
	}

	read := func(name string, s Summary) Summary {
		got, err := ReadSegment(src, name, s, 1<<20)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		return got
	}

	hSum := read("head", summarize(head))
	mSum := read("mid", summarize(mid))
	tSum := read("tail", summarize(tail))

	j, _ := NewJoiner("head", "mid", "tail")
	_ = j.Set("head", hSum)
	_ = j.Set("mid", mSum)
	_ = j.Set("tail", tSum)

	want := summarize(whole(head, mid, tail))
	if err := j.Verify(want); err != nil {
		t.Fatalf("initial verify: %v", err)
	}

	// Replace ONLY the middle bytes. From here on opening head/tail must be
	// impossible: their bytes are never rescanned.
	newMid := []byte("a brand new middle, different length!!")
	src.data["mid"] = newMid
	src.panics["head"] = true
	src.panics["tail"] = true

	if err := j.Replace("mid", read("mid", summarize(newMid))); err != nil {
		t.Fatal(err)
	}
	if err := j.Verify(summarize(whole(head, newMid, tail))); err != nil {
		t.Fatalf("post-replacement verify: %v", err)
	}
	if src.opens["head"] != 1 || src.opens["tail"] != 1 {
		t.Fatalf("unchanged segments rescanned: %v", src.opens)
	}
	if src.opens["mid"] != 2 {
		t.Fatalf("middle should be read exactly twice, got %d", src.opens["mid"])
	}
}

func TestReadSegmentLargeStreamSmallBuffer(t *testing.T) {
	// More than readChunk data streams through one 64KiB buffer; arbitrary
	// internal chunking must not affect the result.
	rng := rand.New(rand.NewSource(3))
	data := make([]byte, readChunk*3+12345)
	_, _ = rng.Read(data)
	s := summarize(data)
	src := SourceFunc(func(name string) (io.Reader, bool) {
		return bytes.NewReader(data), true
	})
	got, err := ReadSegment(src, "big", s, MaxLen)
	if err != nil {
		t.Fatal(err)
	}
	if got != s {
		t.Fatalf("%s != %s", got.Encode(), s.Encode())
	}
}

func ExampleCombine() {
	a := summarize([]byte("foo"))
	b := summarize([]byte("bar"))
	combined, _ := Combine(a, b)
	direct := summarize([]byte("foobar"))
	fmt.Println(combined == direct)
	// Output: true
}

func TestHashSegmentAndRoundTripVerify(t *testing.T) {
	data := []byte("store-and-verify-me")
	src := SourceFunc(func(name string) (io.Reader, bool) {
		return bytes.NewReader(data), true
	})
	got, err := HashSegment(src, "s", int64(len(data)), MaxLen)
	if err != nil {
		t.Fatal(err)
	}
	if got != summarize(data) {
		t.Fatal("HashSegment digest mismatch")
	}
	// Re-verifying with the produced summary must pass.
	if _, err := ReadSegment(src, "s", got, MaxLen); err != nil {
		t.Fatalf("verify own summary: %v", err)
	}
	// Empty hash is the identity and never opens the source.
	denied := SourceFunc(func(string) (io.Reader, bool) { return nil, false })
	if e, err := HashSegment(denied, "x", 0, MaxLen); err != nil || e != Empty {
		t.Fatalf("empty hash: %v %v", e, err)
	}
	if _, err := HashSegment(denied, "x", 3, MaxLen); !errors.Is(err, ErrMissingData) {
		t.Fatalf("missing: %v", err)
	}
}
