package main

import (
	"bytes"
	"math/rand"
	"testing"
)

// refCost is an independent reference parser: a plain O(n^2) DP over literal
// runs and matches, written separately from the production parsers. It serves
// as the enumeration reference cross-check for exactParseCost.
func refCost(s []byte, dict [][]byte) int {
	n := len(s)
	const inf = int(^uint(0) >> 1)
	dp := make([]int, n+1)
	for i := n - 1; i >= 0; i-- {
		best := inf
		for l := 1; l <= maxLitRunLen && i+l <= n; l++ {
			if c := litRunHeaderCost + l + dp[i+l]; c < best {
				best = c
			}
		}
		for _, p := range dict {
			if bytes.HasPrefix(s[i:], p) {
				if c := matchTokenCost + dp[i+len(p)]; c < best {
					best = c
				}
			}
		}
		dp[i] = best
	}
	return dp[0]
}

func TestExactParseCostMatchesReference(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	dicts := [][][]byte{
		nil,
		{[]byte("ab")},
		{[]byte("ab"), []byte("ba"), []byte("aba")},
		{[]byte{0}, []byte{0, 0}, []byte{1, 0}},
	}
	alphabets := [][]byte{[]byte("ab"), []byte("abc"), {0, 1}}
	for _, dict := range dicts {
		di := buildDictIndex(dict)
		for _, alpha := range alphabets {
			for trial := 0; trial < 30; trial++ {
				n := 1 + rng.Intn(40)
				s := make([]byte, n)
				for i := range s {
					s[i] = alpha[rng.Intn(len(alpha))]
				}
				got := exactParseCost(s, di)
				want := refCost(s, dict)
				if got != want {
					t.Fatalf("exactParseCost(%q)=%d, reference=%d", s, got, want)
				}
				// The exact parse must serialize to exactly that size.
				toks := exactParse(s, di)
				stream := encodeStream(0, s, toks)
				// Stream header is magic+dictCRC+2 varints+crc; token bytes are the rest.
				overhead := 4 + 4 + 1 + 1 + 4
				if len(stream)-overhead != got {
					t.Fatalf("exactParse(%q) tokens serialize to %d token bytes, cost model says %d", s, len(stream)-overhead, got)
				}
			}
		}
	}
}

func TestFastParseTokensReproduceInput(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	dict := [][]byte{[]byte("the "), []byte("quick "), []byte("fox")}
	di := buildDictIndex(dict)
	for trial := 0; trial < 50; trial++ {
		words := [][]byte{[]byte("the "), []byte("quick "), []byte("fox"), []byte("lazy "), []byte("dog. ")}
		var buf bytes.Buffer
		for i := 0; i < 1+rng.Intn(30); i++ {
			buf.Write(words[rng.Intn(len(words))])
		}
		src := buf.Bytes()
		var rebuilt bytes.Buffer
		for _, tk := range fastParse(src, di) {
			if tk.match >= 0 {
				rebuilt.Write(dict[tk.match])
			} else {
				rebuilt.Write(tk.literal)
			}
		}
		if !bytes.Equal(rebuilt.Bytes(), src) {
			t.Fatalf("parse does not reproduce input")
		}
	}
}
