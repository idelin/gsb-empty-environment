package pathpat

import (
	"math/rand"
	"testing"
)

func parseAll(ss []string) []Pattern {
	out := make([]Pattern, len(ss))
	for i, s := range ss {
		out[i] = ParsePattern(s)
	}
	return out
}

func TestSolveTable(t *testing.T) {
	cases := []struct {
		name       string
		allow      []string
		deny       []string
		verdict    Verdict
		witnessLen int // meaningful when verdict == Witness
		witness    []string
	}{
		{"allow all", []string{"**"}, nil, Witness, 0, nil},
		{"empty path allowed", []string{""}, nil, Witness, 0, nil},
		{"root allowed", []string{"/"}, nil, Witness, 1, []string{""}},
		{"empty allowed root denied", []string{""}, []string{"/"}, Witness, 0, nil},
		{"root allowed empty denied", []string{"/"}, []string{""}, Witness, 1, []string{""}},
		{"empty path denied, one segment ok", []string{"**"}, []string{""}, Witness, 1, nil},
		{"exact literal denied", []string{"a"}, []string{"a"}, None, 0, nil},
		{"literal denied by glob", []string{"a"}, []string{"**"}, None, 0, nil},
		{"everything denied", []string{"**"}, []string{"**"}, None, 0, nil},
		{"no allow patterns", nil, nil, None, 0, nil},
		{"nested denied", []string{"a/b"}, []string{"a/**"}, None, 0, nil},
		{"prefix denied needs longer", []string{"a/**"}, []string{"a"}, Witness, 2, nil},
		{"literal needs fresh segment", []string{"*"}, []string{"a"}, Witness, 1, []string{"seg0"}},
		{"second segment literal ok", []string{"a/*"}, []string{"a/b"}, Witness, 2, []string{"a", "a"}},
		{"depth three needed", []string{"**"}, []string{"", "*", "*/*"}, Witness, 3, nil},
		{"literal witness", []string{"x"}, nil, Witness, 1, []string{"x"}},
		{"empty first segment", []string{"/*"}, nil, Witness, 2, []string{"", ""}},
		{"star first segment", []string{"*/x"}, []string{"a/x"}, Witness, 2, []string{"x", "x"}},
	}
	for _, c := range cases {
		allow, deny := parseAll(c.allow), parseAll(c.deny)
		res := Solve(allow, deny, Budget{})
		if res.Verdict != c.verdict {
			t.Errorf("%s: verdict = %v, want %v", c.name, res.Verdict, c.verdict)
			continue
		}
		if c.verdict != Witness {
			continue
		}
		if len(res.Witness) != c.witnessLen {
			t.Errorf("%s: witness %q has %d segments, want %d", c.name, res.Witness, len(res.Witness), c.witnessLen)
		}
		if c.witness != nil && !equalStrs(res.Witness, c.witness) {
			t.Errorf("%s: witness = %q, want %q", c.name, res.Witness, c.witness)
		}
		if !AllowedAndNotDenied(allow, deny, res.Witness) {
			t.Errorf("%s: witness %q is not allowed-and-not-denied", c.name, res.Witness)
		}
	}
}

func equalStrs(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestSolveBudget(t *testing.T) {
	// Shortest witness needs 3 segments.
	allow := parseAll([]string{"**"})
	deny := parseAll([]string{"", "*", "*/*"})

	if res := Solve(allow, deny, Budget{MaxDepth: 2}); res.Verdict != Unknown {
		t.Errorf("MaxDepth=2: verdict = %v, want unknown", res.Verdict)
	}
	if res := Solve(allow, deny, Budget{MaxDepth: 3}); res.Verdict != Witness || len(res.Witness) != 3 {
		t.Errorf("MaxDepth=3: got %v %q, want 3-segment witness", res.Verdict, res.Witness)
	}
	if res := Solve(allow, deny, Budget{MaxExpansions: 2}); res.Verdict != Unknown {
		t.Errorf("MaxExpansions=2: verdict = %v, want unknown", res.Verdict)
	}
	if res := Solve(allow, deny, Budget{MaxExpansions: 3}); res.Verdict != Witness || len(res.Witness) != 3 {
		t.Errorf("MaxExpansions=3: got %v %q, want 3-segment witness", res.Verdict, res.Witness)
	}

	// Witness at depth 2 needs 2 expansions.
	deny2 := parseAll([]string{"", "*"})
	if res := Solve(allow, deny2, Budget{MaxExpansions: 1}); res.Verdict != Unknown {
		t.Errorf("MaxExpansions=1: verdict = %v, want unknown", res.Verdict)
	}
	if res := Solve(allow, deny2, Budget{MaxExpansions: 2}); res.Verdict != Witness || len(res.Witness) != 2 {
		t.Errorf("MaxExpansions=2: got %v %q, want 2-segment witness", res.Verdict, res.Witness)
	}

	// Budget does not corrupt a provable None.
	if res := Solve(parseAll([]string{"a"}), parseAll([]string{"a"}), Budget{}); res.Verdict != None {
		t.Errorf("unlimited: verdict = %v, want none", res.Verdict)
	}
}

// TestSolveAgainstBruteForce cross-checks Solve against literal exhaustive
// enumeration of short paths over a small alphabet on random patterns.
func TestSolveAgainstBruteForce(t *testing.T) {
	const maxDepth = 5
	alphabet := []string{"a", "b", "", "zz"}
	var rng *rand.Rand

	genPattern := func() Pattern {
		n := rng.Intn(5)
		p := Pattern{}
		for i := 0; i < n; i++ {
			switch r := rng.Float64(); {
			case r < 0.30:
				p.Segs = append(p.Segs, Seg{Kind: Lit, Lit: string(rune('a' + rng.Intn(2)))})
			case r < 0.35:
				p.Segs = append(p.Segs, Seg{Kind: Lit, Lit: ""})
			case r < 0.65:
				p.Segs = append(p.Segs, Seg{Kind: Star})
			default:
				p.Segs = append(p.Segs, Seg{Kind: StarStar})
			}
		}
		return p
	}
	genSet := func(max int) []Pattern {
		n := rng.Intn(max + 1)
		out := make([]Pattern, n)
		for i := range out {
			out[i] = genPattern()
		}
		return out
	}

	for iter := 0; iter < 1000; iter++ {
		if iter%200 == 0 {
			rng = rand.New(rand.NewSource(int64(iter/200 + 1)))
		}
		allow := genSet(3)
		deny := genSet(3)

		res := Solve(allow, deny, Budget{})
		if res.Verdict == Unknown {
			t.Fatalf("iter %d: unlimited budget returned unknown", iter)
		}
		found, w := BruteForce(allow, deny, alphabet, maxDepth)

		switch res.Verdict {
		case Witness:
			if !AllowedAndNotDenied(allow, deny, res.Witness) {
				t.Fatalf("iter %d: witness %q not allowed-and-not-denied", iter, res.Witness)
			}
			if len(res.Witness) <= maxDepth {
				if !found {
					t.Fatalf("iter %d: Solve found %q but brute force found nothing", iter, res.Witness)
				}
				if len(w) != len(res.Witness) {
					t.Fatalf("iter %d: witness length %d, brute force minimum %d", iter, len(res.Witness), len(w))
				}
			} else if found {
				t.Fatalf("iter %d: Solve minimal length %d but brute force found %q", iter, len(res.Witness), w)
			}
		case None:
			if found {
				t.Fatalf("iter %d: Solve claims none but brute force found %q", iter, w)
			}
		}

		// A depth-limited run must be consistent with the unlimited one.
		lim := Solve(allow, deny, Budget{MaxDepth: 2})
		switch lim.Verdict {
		case Witness:
			if res.Verdict != Witness || len(lim.Witness) != len(res.Witness) {
				t.Fatalf("iter %d: limited witness %q vs unlimited %q", iter, lim.Witness, res.Witness)
			}
		case None:
			if res.Verdict != None {
				t.Fatalf("iter %d: limited claims none, unlimited found %q", iter, res.Witness)
			}
		}
	}
}
