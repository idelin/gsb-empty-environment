package netsec

import (
	"bytes"
	"errors"
	"math/rand"
	"testing"
)

type testToken struct {
	kind    byte
	literal byte
	dist    int
	length  int
	pos     int
}

func parseTokens(t *testing.T, encoded []byte) []testToken {
	t.Helper()
	var tokens []testToken
	pos := 0
	for i := 0; i < len(encoded); {
		start := pos
		switch encoded[i] {
		case 0:
			if i+2 > len(encoded) {
				t.Fatalf("truncated literal at token byte %d", i)
			}
			tokens = append(tokens, testToken{
				kind:    0,
				literal: encoded[i+1],
				pos:     start,
			})
			pos++
			i += 2
		case 1:
			if i+4 > len(encoded) {
				t.Fatalf("truncated match at token byte %d", i)
			}
			dist := int(encoded[i+1])<<8 | int(encoded[i+2])
			length := int(encoded[i+3])
			if dist < 1 || dist > WindowSize {
				t.Fatalf("invalid distance %d at token byte %d", dist, i)
			}
			if length < MinMatch || length > MaxMatch {
				t.Fatalf("invalid length %d at token byte %d", length, i)
			}
			tokens = append(tokens, testToken{
				kind:   1,
				dist:   dist,
				length: length,
				pos:    start,
			})
			pos += length
			i += 4
		default:
			t.Fatalf("invalid token kind %d at byte %d", encoded[i], i)
		}
	}
	return tokens
}

func tokenAt(t *testing.T, tokens []testToken, pos int) testToken {
	t.Helper()
	for _, tok := range tokens {
		if tok.pos == pos {
			return tok
		}
	}
	t.Fatalf("no token starts at output position %d", pos)
	return testToken{}
}

func randomBytes(n int, seed int64) []byte {
	b := make([]byte, n)
	rng := rand.New(rand.NewSource(seed))
	_, _ = rng.Read(b)
	return b
}

func TestCodecRoundTrip(t *testing.T) {
	allBytes := make([]byte, 256)
	for i := range allBytes {
		allBytes[i] = byte(i)
	}

	cases := []struct {
		name string
		dict []byte
		src  []byte
	}{
		{name: "empty"},
		{name: "single", src: []byte("x")},
		{name: "all literal values", src: allBytes},
		{name: "repeated", src: bytes.Repeat([]byte("abc"), 500)},
		{name: "random", src: randomBytes(5000, 1)},
		{name: "dictionary", dict: randomBytes(3000, 2), src: randomBytes(4000, 3)},
		{name: "dictionary suffix used", dict: append(bytes.Repeat([]byte("q"), 5000), []byte("shared-tail-pattern")...), src: []byte("shared-tail-pattern")},
		{name: "cross window", dict: randomBytes(WindowSize+100, 4), src: randomBytes(WindowSize+100, 5)},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			encoded := Compress(tc.dict, tc.src)
			parseTokens(t, encoded)
			got, err := Decompress(tc.dict, encoded)
			if err != nil {
				t.Fatalf("Decompress: %v", err)
			}
			if !bytes.Equal(got, tc.src) {
				t.Fatalf("round trip mismatch: got %d bytes, want %d", len(got), len(tc.src))
			}
		})
	}
}

func TestOverlappingMatch(t *testing.T) {
	src := bytes.Repeat([]byte("a"), 1024)
	encoded := Compress(nil, src)
	tokens := parseTokens(t, encoded)

	if len(tokens) < 2 {
		t.Fatalf("expected literal plus overlapping matches, got %d tokens", len(tokens))
	}
	if tokens[0].kind != 0 || tokens[0].literal != 'a' {
		t.Fatalf("first token = %+v, want literal 'a'", tokens[0])
	}
	if tokens[1].kind != 1 || tokens[1].dist != 1 || tokens[1].length != MaxMatch {
		t.Fatalf("second token = %+v, want dist 1 len %d", tokens[1], MaxMatch)
	}
	if len(encoded) >= 64 {
		t.Fatalf("overlap was not exploited: %d compressed bytes", len(encoded))
	}

	got, err := Decompress(nil, encoded)
	if err != nil {
		t.Fatalf("Decompress: %v", err)
	}
	if !bytes.Equal(got, src) {
		t.Fatal("overlap round trip mismatch")
	}
}

func TestOverlapAcrossDictionaryBoundary(t *testing.T) {
	dict := []byte("aaaa")
	src := bytes.Repeat([]byte("a"), 600)
	encoded := Compress(dict, src)
	first := tokenAt(t, parseTokens(t, encoded), 0)
	if first.kind != 1 || first.dist != 1 || first.length != MaxMatch {
		t.Fatalf("first token = %+v, want cross-boundary match dist 1 len %d", first, MaxMatch)
	}
	got, err := Decompress(dict, encoded)
	if err != nil {
		t.Fatalf("Decompress: %v", err)
	}
	if !bytes.Equal(got, src) {
		t.Fatal("cross-boundary overlap round trip mismatch")
	}
}

func TestMatchLengthBounds(t *testing.T) {
	t.Run("minimum three", func(t *testing.T) {
		tokens := parseTokens(t, Compress(nil, []byte("abab")))
		for _, tok := range tokens {
			if tok.kind == 1 {
				t.Fatalf("two-byte repeat must not produce a match: %+v", tok)
			}
		}

		dict := []byte("abcX")
		first := tokenAt(t, parseTokens(t, Compress(dict, []byte("abc"))), 0)
		if first.kind != 1 || first.length != 3 {
			t.Fatalf("token = %+v, want length-3 match", first)
		}
	})

	t.Run("maximum 255", func(t *testing.T) {
		src := bytes.Repeat([]byte("a"), 300)
		tokens := parseTokens(t, Compress(nil, src))
		foundMax := false
		for _, tok := range tokens {
			if tok.kind != 1 {
				continue
			}
			if tok.length < MinMatch || tok.length > MaxMatch {
				t.Fatalf("match length out of range: %+v", tok)
			}
			if tok.length == MaxMatch {
				foundMax = true
			}
		}
		if !foundMax {
			t.Fatal("expected a 255-byte match")
		}
	})
}

func TestLongestMatchWinsOverNearerShortMatch(t *testing.T) {
	// The nearest candidate shares only the three-byte prefix; the farther
	// candidate shares the full source.
	dict := []byte("ABCDEFGHIJxxxxABCZ")
	src := []byte("ABCDEFGHIJ")
	first := tokenAt(t, parseTokens(t, Compress(dict, src)), 0)
	if first.kind != 1 || first.dist != 18 || first.length != 10 {
		t.Fatalf("token = %+v, want longest match dist 18 len 10", first)
	}
	got, err := Decompress(dict, Compress(dict, src))
	if err != nil {
		t.Fatalf("Decompress: %v", err)
	}
	if !bytes.Equal(got, src) {
		t.Fatal("round trip mismatch")
	}
}

func TestTieBreaksToSmallestDistance(t *testing.T) {
	dict := []byte("aaaa")
	src := []byte("aaaa")
	first := tokenAt(t, parseTokens(t, Compress(dict, src)), 0)
	if first.kind != 1 || first.dist != 1 || first.length != 4 {
		t.Fatalf("token = %+v, want tie broken to dist 1 len 4", first)
	}
}

func TestWindowBoundary(t *testing.T) {
	needle := []byte("NEEDLE-UNIQUE")
	atBoundary := append([]byte(nil), needle...)
	atBoundary = append(atBoundary, bytes.Repeat([]byte("x"), WindowSize-len(needle))...)
	justOutside := append([]byte(nil), needle...)
	justOutside = append(justOutside, bytes.Repeat([]byte("x"), WindowSize)...)

	insideEncoded := Compress(atBoundary, needle)
	inside := tokenAt(t, parseTokens(t, insideEncoded), 0)
	if inside.kind != 1 || inside.dist != WindowSize || inside.length != len(needle) {
		t.Fatalf("boundary token = %+v, want dist %d len %d", inside, WindowSize, len(needle))
	}

	outsideEncoded := Compress(justOutside, needle)
	for _, tok := range parseTokens(t, outsideEncoded) {
		if tok.kind == 1 {
			t.Fatalf("match outside 4096-byte window was emitted: %+v", tok)
		}
	}
	if len(outsideEncoded) != 2*len(needle) {
		t.Fatalf("outside-window output = %d bytes, want %d literals", len(outsideEncoded), len(needle))
	}
}

func TestWindowSlidesWithinStream(t *testing.T) {
	needle := []byte("UNIQUE-MARKER-42")
	filler := func(n int) []byte { return bytes.Repeat([]byte("z"), n) }

	near := append(append([]byte(nil), needle...), filler(1000)...)
	near = append(near, needle...)
	nearTokens := parseTokens(t, Compress(nil, near))
	nearToken := tokenAt(t, nearTokens, len(needle)+1000)
	if nearToken.kind != 1 {
		t.Fatalf("near repeat token = %+v, want a match", nearToken)
	}

	far := append(append([]byte(nil), needle...), filler(WindowSize+1000)...)
	far = append(far, needle...)
	farTokens := parseTokens(t, Compress(nil, far))
	farToken := tokenAt(t, farTokens, len(needle)+WindowSize+1000)
	if farToken.kind != 0 {
		t.Fatalf("far repeat token = %+v, must not reach past the window", farToken)
	}

	for _, src := range [][]byte{near, far} {
		got, err := Decompress(nil, Compress(nil, src))
		if err != nil {
			t.Fatalf("Decompress: %v", err)
		}
		if !bytes.Equal(got, src) {
			t.Fatal("window-slide round trip mismatch")
		}
	}
}

func TestDecompressRejectsCorruptStreams(t *testing.T) {
	cases := []struct {
		name string
		enc  []byte
	}{
		{name: "truncated literal", enc: []byte{0}},
		{name: "trailing truncated literal", enc: []byte{0, 'a', 0}},
		{name: "truncated match", enc: []byte{1, 0, 1}},
		{name: "zero distance", enc: []byte{1, 0, 0, 3}},
		{name: "distance outside window", enc: []byte{1, 0x10, 0x01, 3}},
		{name: "length below minimum", enc: []byte{0, 'a', 0, 'b', 0, 'c', 1, 0, 1, 2}},
		{name: "distance beyond history", enc: []byte{1, 0, 1, 3}},
		{name: "unknown token", enc: []byte{2}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := Decompress(nil, tc.enc); !errors.Is(err, ErrCorrupt) {
				t.Fatalf("error = %v, want ErrCorrupt", err)
			}
		})
	}
}

func referenceCompress(dictionary, src []byte) []byte {
	base := tailWindow(dictionary)
	all := make([]byte, 0, len(base)+len(src))
	all = append(all, base...)
	all = append(all, src...)

	out := make([]byte, 0, len(src)+len(src)/8)
	for pos := len(base); pos < len(all); {
		maxLen := len(all) - pos
		if maxLen > MaxMatch {
			maxLen = MaxMatch
		}
		bestLen := 0
		bestDist := 0
		if maxLen >= MinMatch {
			maxDist := pos
			if maxDist > WindowSize {
				maxDist = WindowSize
			}
			// Ascending distance means ties keep the smallest distance.
			for dist := 1; dist <= maxDist; dist++ {
				cand := pos - dist
				n := 0
				for n < maxLen && all[cand+n] == all[pos+n] {
					n++
				}
				if n > bestLen {
					bestLen = n
					bestDist = dist
					if n == maxLen {
						break
					}
				}
			}
		}
		if bestLen >= MinMatch {
			out = append(out, 1, byte(bestDist>>8), byte(bestDist), byte(bestLen))
			pos += bestLen
		} else {
			out = append(out, 0, all[pos])
			pos++
		}
	}
	return out
}

func TestCodecMatchesReferenceSelection(t *testing.T) {
	rng := rand.New(rand.NewSource(12345))
	alphabets := [][]byte{
		[]byte("a"),
		[]byte("ab"),
		[]byte("abc"),
		[]byte("abcd"),
		[]byte("abcdefghij"),
	}

	for i := 0; i < 600; i++ {
		dictLen := rng.Intn(80)
		srcLen := rng.Intn(120)
		dict := make([]byte, dictLen)
		src := make([]byte, srcLen)
		alphabet := alphabets[rng.Intn(len(alphabets))]
		for j := range dict {
			dict[j] = alphabet[rng.Intn(len(alphabet))]
		}
		for j := range src {
			src[j] = alphabet[rng.Intn(len(alphabet))]
		}

		got := Compress(dict, src)
		want := referenceCompress(dict, src)
		if !bytes.Equal(got, want) {
			t.Fatalf("case %d mismatch\ndict=%q\nsrc=%q\ngot=%x\nwant=%x", i, dict, src, got, want)
		}
		decoded, err := Decompress(dict, got)
		if err != nil {
			t.Fatalf("case %d decode: %v", i, err)
		}
		if !bytes.Equal(decoded, src) {
			t.Fatalf("case %d round trip mismatch", i)
		}
	}
}
