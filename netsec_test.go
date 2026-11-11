package netsec

import (
	"bytes"
	"encoding/base64"
	"encoding/hex"
	"strings"
	"testing"
)

func spanOf(t *testing.T, text, sub string) Range {
	t.Helper()
	i := strings.Index(text, sub)
	if i < 0 {
		t.Fatalf("substring %q not found in %q", sub, text)
	}
	return Range{i, i + len(sub)}
}

func findChain(res Result, wantEncs ...Encoding) *Finding {
	for i := range res.Findings {
		f := &res.Findings[i]
		if len(f.Chain) != len(wantEncs) {
			continue
		}
		ok := true
		for j, enc := range wantEncs {
			if f.Chain[j].Encoding != enc {
				ok = false
				break
			}
		}
		if ok {
			return f
		}
	}
	return nil
}

// Overlapping caller candidates must each yield a finding, and the reported
// cover must be the minimal original range of the secret, not the whole
// candidate.
func TestOverlappingCandidates(t *testing.T) {
	secret := []byte("hunter2")
	outer := hex.EncodeToString([]byte("hunter20")) // 16 hex chars
	inner := outer[:14]                             // hex of "hunter2"
	text := "<<" + outer + ">>"
	cOuter := Range{2, 2 + len(outer)}
	cInner := Range{2, 2 + len(inner)}

	res := Scan([]byte(text), [][]byte{secret}, []Range{cOuter, cInner}, Options{})
	if len(res.Findings) != 2 {
		t.Fatalf("want 2 findings, got %d: %+v", len(res.Findings), res.Findings)
	}
	srcs := make(map[Range]bool)
	for _, f := range res.Findings {
		// Secret bytes [0,7) map to hex chars [0,14) inside each candidate.
		if want := (Range{2, 16}); f.Cover != want {
			t.Errorf("cover = %v, want minimal %v", f.Cover, want)
		}
		if len(f.Chain) != 1 || f.Chain[0].Encoding != EncHex {
			t.Errorf("unexpected chain: %+v", f.Chain)
		}
		srcs[f.Chain[0].Src] = true
		out, err := Replay([]byte(text), f.Chain)
		if err != nil || !bytes.Contains(out, secret) {
			t.Errorf("replay failed: %q, %v", out, err)
		}
	}
	if !srcs[cOuter] || !srcs[cInner] {
		t.Errorf("expected chains from both overlapping candidates, got %v", srcs)
	}
}

// Strict Base64 must reject bad padding and non-canonical trailing bits even
// when a lenient decoder would accept them.
func TestStrictPaddingRejected(t *testing.T) {
	// Premise: a lenient decoder accepts "aGl=" as "hi"; strict must not.
	if out, err := base64.StdEncoding.DecodeString("aGl="); err != nil || string(out) != "hi" {
		t.Fatalf("test premise broken: %q, %v", out, err)
	}
	if _, err := base64.StdEncoding.Strict().DecodeString("aGl="); err == nil {
		t.Fatalf("test premise broken: strict stdlib accepted aGl=")
	}

	secret := []byte("hi")
	text := "aGk= aGk=== aGl= aGk!"
	good := spanOf(t, text, "aGk=")
	cands := []Range{
		good,
		spanOf(t, text, "aGk==="), // excess padding
		spanOf(t, text, "aGl="),   // non-canonical trailing bits
		spanOf(t, text, "aGk!"),   // invalid character
	}
	res := Scan([]byte(text), [][]byte{secret}, cands, Options{})
	if len(res.Findings) != 1 {
		t.Fatalf("want exactly 1 finding, got %d: %+v", len(res.Findings), res.Findings)
	}
	f := res.Findings[0]
	// The secret bytes depend only on "aGk"; the "=" padding is excluded
	// from the minimal cover.
	if want := (Range{good.Start, good.Start + 3}); f.Cover != want {
		t.Errorf("cover = %v, want %v", f.Cover, want)
	}
	if len(f.Chain) != 1 || f.Chain[0].Encoding != EncBase64 {
		t.Errorf("unexpected chain: %+v", f.Chain)
	}
	out, err := Replay([]byte(text), f.Chain)
	if err != nil || string(out) != "hi" {
		t.Errorf("replay = %q, %v; want %q", out, err, "hi")
	}
}

// Identical content decoded from different sources must share one node
// (its inner tokens expanded only once) while every source chain is still
// reported.
func TestSharedNodeDistinctChains(t *testing.T) {
	secret := []byte("sup3rsec")
	b64 := base64.RawStdEncoding.EncodeToString(secret)
	h := hex.EncodeToString([]byte(b64))
	text := "x:" + h + ";y:" + h + ";"
	c1 := Range{2, 2 + len(h)}
	c2 := Range{2 + len(h) + 3, 2 + len(h) + 3 + len(h)}

	s, res := scan([]byte(text), [][]byte{secret}, []Range{c1, c2}, Options{})
	// The shared intermediate node has one incoming edge per source, and
	// the secret node exactly one: the inner token was expanded once.
	mid, ok := s.byContent[b64]
	if !ok {
		t.Fatalf("intermediate node %q not found", b64)
	}
	if len(mid.edges) != 2 {
		t.Errorf("shared node has %d incoming edges, want 2", len(mid.edges))
	}
	leaf, ok := s.byContent[string(secret)]
	if !ok {
		t.Fatalf("secret node not found")
	}
	if len(leaf.edges) != 1 {
		t.Errorf("secret node has %d incoming edges, want 1 (expanded once)", len(leaf.edges))
	}
	if len(res.Findings) != 2 {
		t.Fatalf("want 2 findings (one per source chain), got %d: %+v", len(res.Findings), res.Findings)
	}
	covers := make(map[Range]bool)
	for _, f := range res.Findings {
		if len(f.Chain) != 2 || f.Chain[0].Encoding != EncHex || f.Chain[1].Encoding != EncBase64 {
			t.Errorf("unexpected chain: %+v", f.Chain)
		}
		covers[f.Cover] = true
		out, err := Replay([]byte(text), f.Chain)
		if err != nil || !bytes.Contains(out, secret) {
			t.Errorf("replay failed: %q, %v", out, err)
		}
	}
	if !covers[c1] || !covers[c2] {
		t.Errorf("expected distinct covers for both sources, got %v", covers)
	}
}

// A secret nested under three encodings is found with the full replayable
// chain; four layers exceed the depth limit and are not reported.
func TestThreeLayerChainAndDepthLimit(t *testing.T) {
	secret := []byte("s3cr3t!")
	l1 := base64.StdEncoding.EncodeToString(secret)
	l2 := hex.EncodeToString([]byte(l1))
	l3 := base64.StdEncoding.EncodeToString([]byte(l2))
	text := "token=" + l3 + ";"
	c := spanOf(t, text, l3)

	res := Scan([]byte(text), [][]byte{secret}, []Range{c}, Options{})
	f := findChain(res, EncBase64, EncHex, EncBase64)
	if f == nil {
		t.Fatalf("no 3-layer finding: %+v", res.Findings)
	}
	if f.Cover.Start != c.Start || f.Cover.End > c.End {
		t.Errorf("cover %v not within candidate %v", f.Cover, c)
	}
	out, err := Replay([]byte(text), f.Chain)
	if err != nil || !bytes.Equal(out, secret) {
		t.Errorf("replay = %q, %v; want %q", out, err, secret)
	}

	// Fourth layer: hex(b64(hex(b64(secret)))) is beyond the 3-layer limit.
	l4 := hex.EncodeToString([]byte(l3))
	text4 := "token=" + l4 + ";"
	c4 := spanOf(t, text4, l4)
	res4 := Scan([]byte(text4), [][]byte{secret}, []Range{c4}, Options{})
	if len(res4.Findings) != 0 {
		t.Errorf("depth limit violated, got %+v", res4.Findings)
	}
}

// When the expansion budget runs out, remaining candidates are returned as
// unchecked ranges.
func TestBudgetExhaustion(t *testing.T) {
	secret := []byte("s3cr3t!")
	// Odd-length payload: the hex form is not valid strict Base64, so each
	// candidate costs exactly one expansion.
	h := hex.EncodeToString([]byte("id:" + string(secret) + "!"))
	text := h + "|" + h + "|" + h
	c1 := Range{0, len(h)}
	c2 := Range{len(h) + 1, 2*len(h) + 1}
	c3 := Range{2*len(h) + 2, 3*len(h) + 2}

	res := Scan([]byte(text), [][]byte{secret}, []Range{c1, c2, c3}, Options{MaxExpansions: 2})
	if res.Expansions != 2 {
		t.Fatalf("expansions = %d, want 2", res.Expansions)
	}
	if len(res.Findings) != 2 {
		t.Fatalf("want 2 findings from the expanded candidates, got %d", len(res.Findings))
	}
	found := false
	for _, u := range res.Unchecked {
		if u.NodeID == 0 && u.Range == c3 && u.Encoding == EncHex {
			found = true
		}
	}
	if !found {
		t.Errorf("unchecked ranges %v do not contain candidate c3", res.Unchecked)
	}
}
