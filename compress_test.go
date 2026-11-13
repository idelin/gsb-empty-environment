package netsec

import (
	"math/rand"
	"reflect"
	"testing"
)

// referenceTokens is an independent, position-by-position transcription
// of the task's compression format, used to cross-check Parse.
func referenceTokens(data []byte) []Token {
	var out []Token
	for i := 0; i < len(data); {
		bestLen, bestDist := 0, 0
		for dist := 1; dist <= WindowSize && dist <= i; dist++ {
			j := i - dist
			l := 0
			for l < MaxMatch && l < dist && i+l < len(data) && data[j+l] == data[i+l] {
				l++
			}
			if l >= MinMatch && l > bestLen {
				bestLen, bestDist = l, dist
			}
		}
		if bestLen > 0 {
			out = append(out, Token{Length: bestLen, Distance: bestDist})
			i += bestLen
		} else {
			out = append(out, Token{Literal: data[i]})
			i++
		}
	}
	return out
}

func TestParseMatchesReference(t *testing.T) {
	rng := rand.New(rand.NewSource(20261114))
	alphabets := [][]byte{
		{'a', 'b'},                     // match-heavy
		{'a', 'b', 'c', 0xFF},          // include separator byte
		{'x', 'y', 'z', 'w', 'v', 'u'}, // sparser matches
	}
	for _, alphabet := range alphabets {
		for trial := 0; trial < 500; trial++ {
			n := rng.Intn(65)
			data := make([]byte, n)
			for k := range data {
				data[k] = alphabet[rng.Intn(len(alphabet))]
			}
			got, want := Parse(data), referenceTokens(data)
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("data=%q:\n got=%v\nwant=%v", data, got, want)
			}
			sum := 0
			for _, tok := range got {
				sum += tok.Cost()
			}
			if sum != CompressLen(data) {
				t.Fatalf("data=%q: CompressLen=%d, token cost sum=%d", data, CompressLen(data), sum)
			}
		}
	}
}

func TestCompressLenEdgeCases(t *testing.T) {
	if got := CompressLen(nil); got != 0 {
		t.Fatalf("empty input: got %d, want 0", got)
	}
	if got := CompressLen([]byte("abc")); got != 3 {
		t.Fatalf("no matches: got %d, want 3", got)
	}
}

func TestOverlapForbidden(t *testing.T) {
	// A short run of identical bytes cannot match: distance 1 caps the
	// non-overlapping match length at 1, below MinMatch, and the run is
	// too short to match against its own first three bytes.
	tokens := Parse([]byte("aaaaa"))
	for _, tok := range tokens {
		if tok.IsMatch() {
			t.Fatalf("aaaaa produced a match token: %v", tokens)
		}
	}
	if got := CompressLen([]byte("aaaaa")); got != 5 {
		t.Fatalf("aaaaa: got %d, want 5 literals", got)
	}

	// Once three full bytes precede the position, a length-3 match at
	// distance 3 is legal (it does not overlap); longer self-overlapping
	// matches remain forbidden.
	wantRun := []Token{
		{Literal: 'a'}, {Literal: 'a'}, {Literal: 'a'},
		{Length: 3, Distance: 3},
	}
	if got := Parse([]byte("aaaaaa")); !reflect.DeepEqual(got, wantRun) {
		t.Fatalf("aaaaaa:\n got=%v\nwant=%v", got, wantRun)
	}

	// A period-3 repeat: at position 3 the match length is capped by the
	// distance (3); at position 6 a non-overlapping length-6 match at
	// distance 6 exists and wins over the shorter distance-3 match.
	tokens = Parse([]byte("abcabcabcabc"))
	want := []Token{
		{Literal: 'a'}, {Literal: 'b'}, {Literal: 'c'},
		{Length: 3, Distance: 3},
		{Length: 6, Distance: 6},
	}
	if !reflect.DeepEqual(tokens, want) {
		t.Fatalf("abcabcabcabc:\n got=%v\nwant=%v", tokens, want)
	}
	if got := CompressLen([]byte("abcabcabcabc")); got != 7 {
		t.Fatalf("abcabcabcabc: got %d, want 7", got)
	}
}

func TestDistanceTieBreaksToNearest(t *testing.T) {
	// At the final "abc" both distance 3 and distance 7 give a length-3
	// match; the nearest distance must win.
	tokens := Parse([]byte("abcXabcabc"))
	want := []Token{
		{Literal: 'a'}, {Literal: 'b'}, {Literal: 'c'}, {Literal: 'X'},
		{Length: 3, Distance: 4},
		{Length: 3, Distance: 3},
	}
	if !reflect.DeepEqual(tokens, want) {
		t.Fatalf("abcXabcabc:\n got=%v\nwant=%v", tokens, want)
	}
}

func TestRepeatsShorterThanThreeAreLiterals(t *testing.T) {
	tokens := Parse([]byte("ababab"))
	for _, tok := range tokens {
		if tok.IsMatch() {
			t.Fatalf("ababab produced a match token: %v", tokens)
		}
	}
	if got := CompressLen([]byte("ababab")); got != 6 {
		t.Fatalf("ababab: got %d, want 6 literals", got)
	}

	// A length-3 repeat does match.
	want := []Token{
		{Literal: 'a'}, {Literal: 'a'}, {Literal: 'b'},
		{Length: 3, Distance: 3},
	}
	if got := Parse([]byte("aabaab")); !reflect.DeepEqual(got, want) {
		t.Fatalf("aabaab:\n got=%v\nwant=%v", got, want)
	}
}

func TestMatchLengthCappedAtFifteen(t *testing.T) {
	data := []byte("0123456789abcdef") // 16 distinct bytes
	data = append(data, data...)       // 32 bytes, second half fully matchable
	tokens := Parse(data)
	var matchLens []int
	for _, tok := range tokens {
		if tok.IsMatch() {
			matchLens = append(matchLens, tok.Length)
		}
	}
	// The 16-byte repeat is split into a 15-byte match plus one literal.
	want := []int{15}
	if !reflect.DeepEqual(matchLens, want) {
		t.Fatalf("match lengths: got %v, want %v (tokens=%v)", matchLens, want, tokens)
	}
	if got := CompressLen(data); got != 16+2+1 {
		t.Fatalf("compressed length: got %d, want 19", got)
	}
}

func TestWindowLimitedToThirtyTwo(t *testing.T) {
	// Distinct filler bytes (none equal to 'a','b','c') so the filler
	// itself never produces a match.
	// "abc" + 32 filler + "abc": the first occurrence is 35 bytes back
	// at the final "abc", outside the 32-byte window, so no match.
	data := []byte("abc")
	for i := 0; i < 32; i++ {
		data = append(data, byte(1+i))
	}
	data = append(data, "abc"...)
	if got, want := CompressLen(data), 38; got != want {
		t.Fatalf("got %d, want %d (all literals)", got, want)
	}
	// With 29 filler bytes the distance is exactly 32 and the match is taken.
	data = []byte("abc")
	for i := 0; i < 29; i++ {
		data = append(data, byte(1+i))
	}
	data = append(data, "abc"...)
	if got, want := CompressLen(data), 35-3+2; got != want {
		t.Fatalf("got %d, want %d", got, want)
	}
}
