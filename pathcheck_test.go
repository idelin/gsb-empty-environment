package pathcheck

import (
	"math/rand"
	"testing"
)

func TestParsePattern(t *testing.T) {
	cases := []struct {
		in     string
		rooted bool
		segs   []Segment
	}{
		{"", false, nil},
		{"/", true, nil},
		{"a", false, []Segment{{Kind: Lit, Lit: "a"}}},
		{"/a/b", true, []Segment{{Kind: Lit, Lit: "a"}, {Kind: Lit, Lit: "b"}}},
		{"*", false, []Segment{{Kind: Star}}},
		{"**", false, []Segment{{Kind: StarStar}}},
		{"a/*/**", false, []Segment{{Kind: Lit, Lit: "a"}, {Kind: Star}, {Kind: StarStar}}},
		{"a*", false, []Segment{{Kind: Lit, Lit: "a*"}}},   // not a wildcard
		{"***", false, []Segment{{Kind: Lit, Lit: "***"}}}, // not a wildcard
	}
	for _, c := range cases {
		p, err := ParsePattern(c.in)
		if err != nil {
			t.Fatalf("ParsePattern(%q): %v", c.in, err)
		}
		if p.Rooted != c.rooted || len(p.Segs) != len(c.segs) {
			t.Fatalf("ParsePattern(%q) = %+v", c.in, p)
		}
		for i, s := range c.segs {
			if p.Segs[i] != s {
				t.Fatalf("ParsePattern(%q) seg %d = %+v, want %+v", c.in, i, p.Segs[i], s)
			}
		}
		if got := p.String(); got != c.in {
			t.Fatalf("Pattern.String() = %q, want %q", got, c.in)
		}
	}
	for _, bad := range []string{"a//b", "a/", "//", "/a/"} {
		if _, err := ParsePattern(bad); err == nil {
			t.Fatalf("ParsePattern(%q) should fail", bad)
		}
	}
	if _, err := ParsePath("a/*"); err == nil {
		t.Fatal("ParsePath should reject wildcards")
	}
}

func TestMatches(t *testing.T) {
	cases := []struct {
		pat, path string
		want      bool
	}{
		{"", "", true},
		{"", "/", false}, // empty path and root path are distinct
		{"/", "", false},
		{"/", "/", true},
		{"a", "a", true},
		{"a", "b", false},
		{"a", "a/b", false},
		{"*", "a", true},
		{"*", "", false},    // * needs exactly one segment
		{"*", "a/b", false}, // * never crosses the separator
		{"**", "", true},
		{"**", "a", true},
		{"**", "a/b/c", true},
		{"a/**", "a", true},
		{"a/**", "a/b/c", true},
		{"a/**", "b", false},
		{"a/*/c", "a/b/c", true},
		{"a/*/c", "a/c", false},
		{"a/*/c", "a/b/d/c", false},
		{"a/**/c", "a/c", true},
		{"a/**/c", "a/b/d/c", true},
		{"/a", "/a", true},
		{"/a", "a", false},
		{"a", "/a", false},
		{"a*", "a*", true}, // "a*" is a literal segment
		{"a*", "ab", false},
	}
	for _, c := range cases {
		pat := MustPattern(c.pat)
		path, err := ParsePath(c.path)
		if err != nil {
			t.Fatalf("ParsePath(%q): %v", c.path, err)
		}
		if got := pat.Matches(path); got != c.want {
			t.Errorf("%q matches %q = %v, want %v", c.pat, c.path, got, c.want)
		}
	}
}

func mustPatterns(t *testing.T, ss []string) []Pattern {
	t.Helper()
	var ps []Pattern
	for _, s := range ss {
		p, err := ParsePattern(s)
		if err != nil {
			t.Fatalf("ParsePattern(%q): %v", s, err)
		}
		ps = append(ps, p)
	}
	return ps
}

func TestCheck(t *testing.T) {
	const big = 1 << 22
	cases := []struct {
		name        string
		allows      []string
		denies      []string
		wantVerdict Verdict
		wantSegs    int  // expected witness segment count (Witness only)
		wantRooted  bool // expected witness rootedness (Witness only)
	}{
		{"literal allowed", []string{"a"}, nil, Witness, 1, false},
		{"empty allow set", nil, []string{"**"}, None, 0, false},
		{"allow all deny all", []string{"**"}, []string{"**"}, None, 0, false},
		{"empty path witness", []string{""}, nil, Witness, 0, false},
		{"root distinct from empty", []string{"/"}, []string{""}, Witness, 0, true},
		{"star needs fresh literal", []string{"*"}, []string{"a"}, Witness, 1, false},
		{"denied literal falls back", []string{"a/*"}, []string{"a/b"}, Witness, 2, false},
		{"starstar zero then one", []string{"a/**"}, []string{"a"}, Witness, 2, false},
		{"shortest witness wins", []string{"x/y", "a"}, nil, Witness, 1, false},
		{"rooted patterns", []string{"/a/*"}, []string{"/a/b"}, Witness, 2, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			allows := mustPatterns(t, c.allows)
			denies := mustPatterns(t, c.denies)
			res := Check(allows, denies, big)
			if res.Verdict != c.wantVerdict {
				t.Fatalf("verdict = %v, want %v", res.Verdict, c.wantVerdict)
			}
			if res.Verdict != Witness {
				return
			}
			if len(res.Path.Segs) != c.wantSegs || res.Path.Rooted != c.wantRooted {
				t.Fatalf("witness = %q (%d segs), want %d segs rooted=%v",
					res.Path.String(), len(res.Path.Segs), c.wantSegs, c.wantRooted)
			}
			if !matchesAny(allows, res.Path) || matchesAny(denies, res.Path) {
				t.Fatalf("witness %q is not allowed-and-not-denied", res.Path.String())
			}
		})
	}
}

func TestCheckUnknown(t *testing.T) {
	// allow {a}, deny {a}: no witness exists, but the depth bound is 16
	// over a 2-symbol alphabet, so a tiny budget must give up.
	allows := mustPatterns(t, []string{"a"})
	denies := mustPatterns(t, []string{"a"})
	if res := Check(allows, denies, 3); res.Verdict != Unknown {
		t.Fatalf("verdict = %v, want Unknown", res.Verdict)
	}
	if res := Check(allows, denies, 1<<22); res.Verdict != None {
		t.Fatalf("verdict = %v, want None", res.Verdict)
	}
	// Zero budget cannot even look at the empty path.
	if res := Check(mustPatterns(t, []string{"**"}), nil, 0); res.Verdict != Unknown {
		t.Fatalf("verdict = %v, want Unknown", res.Verdict)
	}
}

// enumerate calls fn for every path over alpha with at most maxDepth
// segments, unrooted and rooted, in increasing depth order.
func enumerate(alpha []string, maxDepth int, fn func(Path)) {
	for depth := 0; depth <= maxDepth; depth++ {
		for _, rooted := range []bool{false, true} {
			idx := make([]int, depth)
			for {
				path := Path{Rooted: rooted, Segs: make([]string, depth)}
				for i, a := range idx {
					path.Segs[i] = alpha[a]
				}
				fn(path)
				i := depth - 1
				for ; i >= 0; i-- {
					idx[i]++
					if idx[i] < len(alpha) {
						break
					}
					idx[i] = 0
				}
				if i < 0 {
					break
				}
			}
		}
	}
}

// TestCheckExhaustiveVerification re-verifies Check's verdicts with an
// independent brute-force pass over the finite alphabet: a reported
// witness must be valid and no shorter path may qualify; a None verdict
// must hold for every path up to the certifying depth bound.
func TestCheckExhaustiveVerification(t *testing.T) {
	cases := []struct {
		allows, denies []string
	}{
		{[]string{"a"}, []string{"a"}},
		{[]string{"**"}, []string{"**"}},
		{[]string{"*"}, []string{"a"}},
		{[]string{"a/*"}, []string{"a/b"}},
		{[]string{"a/**/c"}, []string{"a/b/c"}},
		{[]string{"", "/"}, []string{""}},
		{[]string{"*/*"}, []string{"a/*", "*/b"}},
		{[]string{"/a"}, []string{"/"}},
	}
	for _, c := range cases {
		allows := mustPatterns(t, c.allows)
		denies := mustPatterns(t, c.denies)
		res := Check(allows, denies, 1<<22)
		alpha := alphabet(allows, denies)
		switch res.Verdict {
		case Witness:
			w := res.Path
			if !matchesAny(allows, w) || matchesAny(denies, w) {
				t.Fatalf("%v/%v: invalid witness %q", c.allows, c.denies, w.String())
			}
			enumerate(alpha, len(w.Segs)-1, func(p Path) {
				if matchesAny(allows, p) && !matchesAny(denies, p) {
					t.Fatalf("%v/%v: shorter witness %q exists, Check returned %q",
						c.allows, c.denies, p.String(), w.String())
				}
			})
		case None:
			enumerate(alpha, res.Bound, func(p Path) {
				if matchesAny(allows, p) && !matchesAny(denies, p) {
					t.Fatalf("%v/%v: None but %q is allowed and not denied",
						c.allows, c.denies, p.String())
				}
			})
		default:
			t.Fatalf("%v/%v: unexpected Unknown with generous budget", c.allows, c.denies)
		}
	}
}

// TestCheckRandomized cross-checks random small pattern sets the same way.
func TestCheckRandomized(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	lits := []string{"a", "b"}
	randPattern := func() Pattern {
		p := Pattern{Rooted: rng.Intn(4) == 0}
		for i, n := 0, rng.Intn(3); i < n; i++ {
			switch rng.Intn(4) {
			case 0:
				p.Segs = append(p.Segs, Segment{Kind: Star})
			case 1:
				p.Segs = append(p.Segs, Segment{Kind: StarStar})
			default:
				p.Segs = append(p.Segs, Segment{Kind: Lit, Lit: lits[rng.Intn(len(lits))]})
			}
		}
		return p
	}
	for trial := 0; trial < 300; trial++ {
		var allows, denies []Pattern
		for i, n := 0, 1+rng.Intn(2); i < n; i++ {
			allows = append(allows, randPattern())
		}
		for i, n := 0, rng.Intn(3); i < n; i++ {
			denies = append(denies, randPattern())
		}
		res := Check(allows, denies, 1<<20)
		alpha := alphabet(allows, denies)
		switch res.Verdict {
		case Witness:
			w := res.Path
			if !matchesAny(allows, w) || matchesAny(denies, w) {
				t.Fatalf("trial %d: invalid witness %q", trial, w.String())
			}
			enumerate(alpha, len(w.Segs)-1, func(p Path) {
				if matchesAny(allows, p) && !matchesAny(denies, p) {
					t.Fatalf("trial %d: shorter witness %q exists", trial, p.String())
				}
			})
		case None:
			// Bound can be huge; verify as far as is cheap.
			depth := res.Bound
			if depth > 10 {
				depth = 10
			}
			enumerate(alpha, depth, func(p Path) {
				if matchesAny(allows, p) && !matchesAny(denies, p) {
					t.Fatalf("trial %d: None but %q qualifies", trial, p.String())
				}
			})
		}
	}
}
