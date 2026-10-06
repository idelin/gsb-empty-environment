package transducer

import (
	"math/rand"
	"testing"
)

func mustCompare(t *testing.T, a, b *Machine, cfg Config) Verdict {
	t.Helper()
	v, err := Compare(a, b, cfg)
	if err != nil {
		t.Fatalf("Compare: %v", err)
	}
	return v
}

// bruteForce enumerates every input string of length exactly maxLen and
// returns the first one whose concatenated outputs differ.
func bruteForce(a, b *Machine, alphabet []rune, maxLen int) (bool, []rune) {
	current := make([]rune, 0, maxLen)
	var walk func() []rune
	walk = func() []rune {
		if len(current) == maxLen {
			if a.Run(current) != b.Run(current) {
				return append([]rune(nil), current...)
			}
			return nil
		}
		for _, sym := range alphabet {
			current = append(current, sym)
			if w := walk(); w != nil {
				return w
			}
			current = current[:len(current)-1]
		}
		return nil
	}
	if w := walk(); w != nil {
		return true, w
	}
	return false, nil
}

func TestRunBasics(t *testing.T) {
	m := &Machine{
		Initial: 0,
		Transitions: map[int]map[rune]Edge{
			0: {'a': {To: 1, Output: "x"}, 'b': {To: 0, Output: ""}},
			1: {'a': {To: 1, Output: "yz"}},
		},
	}
	if r := m.Run([]rune("aa")); r.Output != "xyz" || r.Error {
		t.Fatalf("Run(aa) = %+v", r)
	}
	if r := m.Run([]rune("aba")); r.Output != "x" || !r.Error {
		t.Fatalf("Run(aba) = %+v, want output x with error", r)
	}
	if r := m.Run(nil); r.Output != "" || r.Error {
		t.Fatalf("Run(empty) = %+v", r)
	}
}

// TestDeferredOutputsReconcile builds machines whose per-edge outputs
// differ (B defers output with empty edge outputs) while the concatenated
// totals over complete length-2 inputs coincide.
func TestDeferredOutputsReconcile(t *testing.T) {
	a := &Machine{
		Initial: 0,
		Transitions: map[int]map[rune]Edge{
			0: {'a': {To: 1, Output: "x"}, 'b': {To: 2, Output: "u"}},
			1: {'a': {To: 1, Output: "y"}, 'b': {To: 1, Output: "z"}},
			2: {'a': {To: 1, Output: "x"}, 'b': {To: 2, Output: "w"}},
		},
	}
	b := &Machine{
		Initial: 0,
		Transitions: map[int]map[rune]Edge{
			0: {'a': {To: 1, Output: ""}, 'b': {To: 2, Output: "u"}},
			1: {'a': {To: 1, Output: "xy"}, 'b': {To: 1, Output: "xz"}},
			2: {'a': {To: 1, Output: "x"}, 'b': {To: 2, Output: "w"}},
		},
	}
	cfg := Config{Alphabet: []rune("ab"), MaxLen: 2}
	if v := mustCompare(t, a, b, cfg); v.Status != Equal {
		t.Fatalf("MaxLen=2: got %v (witness %q), want Equal", v.Status, v.Witness)
	}
	// With bound 1 the deferred output never catches up: "a" yields
	// "x" vs "".
	v := mustCompare(t, a, b, Config{Alphabet: []rune("ab"), MaxLen: 1})
	if v.Status != NotEqual || string(v.Witness) != "a" {
		t.Fatalf("MaxLen=1: got %v witness %q, want NotEqual witness \"a\"", v.Status, v.Witness)
	}
	// Bound 3 diverges again: "aaa" yields "xyz" vs "xyxy".
	v = mustCompare(t, a, b, Config{Alphabet: []rune("ab"), MaxLen: 3})
	if v.Status != NotEqual || len(v.Witness) != 3 || a.Run(v.Witness) == b.Run(v.Witness) {
		t.Fatalf("MaxLen=3: got %v witness %q", v.Status, v.Witness)
	}
}

func TestMissingTransitionIsErrorOutput(t *testing.T) {
	withB := &Machine{
		Initial:     0,
		Transitions: map[int]map[rune]Edge{0: {'a': {To: 0, Output: "x"}, 'b': {To: 0, Output: "y"}}},
	}
	withoutB := &Machine{
		Initial:     0,
		Transitions: map[int]map[rune]Edge{0: {'a': {To: 0, Output: "x"}}},
	}
	cfg := Config{Alphabet: []rune("ab"), MaxLen: 1}
	v := mustCompare(t, withB, withoutB, cfg)
	if v.Status != NotEqual || string(v.Witness) != "b" {
		t.Fatalf("got %v witness %q, want NotEqual witness \"b\"", v.Status, v.Witness)
	}

	// Both machines missing the same transition error identically.
	otherWithoutB := &Machine{
		Initial:     0,
		Transitions: map[int]map[rune]Edge{0: {'a': {To: 0, Output: "x"}}},
	}
	v = mustCompare(t, withoutB, otherWithoutB, Config{Alphabet: []rune("ab"), MaxLen: 3})
	if v.Status != Equal {
		t.Fatalf("identical missing transitions: got %v, want Equal", v.Status)
	}

	// Same error timing but different output before the error.
	late := &Machine{
		Initial:     0,
		Transitions: map[int]map[rune]Edge{0: {'a': {To: 1, Output: ""}}, 1: {}},
	}
	lateLoud := &Machine{
		Initial:     0,
		Transitions: map[int]map[rune]Edge{0: {'a': {To: 1, Output: "x"}}, 1: {}},
	}
	v = mustCompare(t, late, lateLoud, Config{Alphabet: []rune("a"), MaxLen: 2})
	if v.Status != NotEqual || string(v.Witness) != "aa" {
		t.Fatalf("got %v witness %q, want NotEqual witness \"aa\"", v.Status, v.Witness)
	}
}

func TestBudgetExhaustionAndResume(t *testing.T) {
	a := &Machine{
		Initial: 0,
		Transitions: map[int]map[rune]Edge{
			0: {'a': {To: 1, Output: "p"}, 'b': {To: 2, Output: ""}},
			1: {'a': {To: 0, Output: ""}, 'b': {To: 2, Output: "q"}},
			2: {'a': {To: 2, Output: "r"}, 'b': {To: 1, Output: ""}},
		},
	}
	b := &Machine{
		Initial: 0,
		Transitions: map[int]map[rune]Edge{
			0: {'a': {To: 1, Output: "p"}, 'b': {To: 2, Output: ""}},
			1: {'a': {To: 0, Output: ""}, 'b': {To: 2, Output: "q"}},
			2: {'a': {To: 2, Output: "r"}, 'b': {To: 1, Output: ""}},
		},
	}
	cfg := Config{Alphabet: []rune("ab"), MaxLen: 5}

	v := mustCompare(t, a, b, Config{Alphabet: cfg.Alphabet, MaxLen: cfg.MaxLen, Budget: 1})
	if v.Status != Unknown || v.Frontier == nil || len(v.Frontier.Items) == 0 {
		t.Fatalf("budget 1: got %v (frontier %v), want Unknown with frontier", v.Status, v.Frontier)
	}

	// Resume in small budget chunks until the check terminates.
	frontier := v.Frontier
	steps := 0
	for {
		steps++
		if steps > 1000 {
			t.Fatal("resume loop did not terminate")
		}
		v, err := Resume(a, b, Config{Alphabet: cfg.Alphabet, MaxLen: cfg.MaxLen, Budget: 3}, frontier)
		if err != nil {
			t.Fatalf("Resume: %v", err)
		}
		if v.Status != Unknown {
			if v.Status != Equal {
				t.Fatalf("resumed check: got %v, want Equal", v.Status)
			}
			break
		}
		frontier = v.Frontier
	}

	// One-shot unlimited run agrees.
	if v := mustCompare(t, a, b, cfg); v.Status != Equal {
		t.Fatalf("unlimited: got %v, want Equal", v.Status)
	}
}

func TestResumePreservesNotEqual(t *testing.T) {
	a := &Machine{
		Initial:     0,
		Transitions: map[int]map[rune]Edge{0: {'a': {To: 0, Output: "x"}, 'b': {To: 0, Output: ""}}},
	}
	b := &Machine{
		Initial:     0,
		Transitions: map[int]map[rune]Edge{0: {'a': {To: 0, Output: ""}, 'b': {To: 0, Output: "y"}}},
	}
	cfg := Config{Alphabet: []rune("ab"), MaxLen: 2, Budget: 1}
	v := mustCompare(t, a, b, cfg)
	for v.Status == Unknown {
		v, _ = Resume(a, b, Config{Alphabet: cfg.Alphabet, MaxLen: cfg.MaxLen, Budget: 1}, v.Frontier)
	}
	if v.Status != NotEqual {
		t.Fatalf("got %v, want NotEqual", v.Status)
	}
	if len(v.Witness) != cfg.MaxLen || a.Run(v.Witness) == b.Run(v.Witness) {
		t.Fatalf("bad witness %q", v.Witness)
	}
}

func TestValidation(t *testing.T) {
	good := &Machine{Initial: 0, Transitions: map[int]map[rune]Edge{0: {'a': {To: 0}}}}
	bad := &Machine{Initial: -1}
	if _, err := Compare(good, bad, Config{Alphabet: []rune("a"), MaxLen: 1}); err == nil {
		t.Fatal("expected error for negative initial state")
	}
	if _, err := Compare(good, good, Config{MaxLen: 2}); err == nil {
		t.Fatal("expected error for empty alphabet")
	}
	if _, err := Compare(good, good, Config{Alphabet: []rune("a"), MaxLen: -1}); err == nil {
		t.Fatal("expected error for negative MaxLen")
	}
	if _, err := Resume(good, good, Config{Alphabet: []rune("a"), MaxLen: 1}, nil); err == nil {
		t.Fatal("expected error for nil frontier")
	}
}

// TestCrossValidationRandom compares the product-graph checker against
// brute-force enumeration of all bounded inputs on random machines,
// including machines with empty edge outputs and missing transitions.
func TestCrossValidationRandom(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	outAlphabet := []string{"", "x", "y", "xy"}
	for trial := 0; trial < 400; trial++ {
		alphabet := []rune("abc")[:2+rng.Intn(2)]
		maxLen := 1 + rng.Intn(5)
		mk := func() *Machine {
			states := 1 + rng.Intn(5)
			m := &Machine{Initial: rng.Intn(states), Transitions: map[int]map[rune]Edge{}}
			for s := 0; s < states; s++ {
				for _, sym := range alphabet {
					if rng.Float64() < 0.2 {
						continue // missing transition
					}
					if m.Transitions[s] == nil {
						m.Transitions[s] = map[rune]Edge{}
					}
					m.Transitions[s][sym] = Edge{
						To:     rng.Intn(states),
						Output: outAlphabet[rng.Intn(len(outAlphabet))],
					}
				}
			}
			return m
		}
		a, b := mk(), mk()
		cfg := Config{Alphabet: alphabet, MaxLen: maxLen}

		v := mustCompare(t, a, b, cfg)
		mismatch, _ := bruteForce(a, b, alphabet, maxLen)
		if (v.Status == Equal) == mismatch {
			t.Fatalf("trial %d: checker=%v bruteForce mismatch=%v", trial, v.Status, mismatch)
		}
		if v.Status == NotEqual {
			if len(v.Witness) != maxLen {
				t.Fatalf("trial %d: witness length %d, want %d", trial, len(v.Witness), maxLen)
			}
			if a.Run(v.Witness) == b.Run(v.Witness) {
				t.Fatalf("trial %d: witness %q does not verify", trial, v.Witness)
			}
		}

		// A budgeted run resumed to completion must agree with the
		// one-shot verdict.
		limited := mustCompare(t, a, b, Config{Alphabet: alphabet, MaxLen: maxLen, Budget: 1 + rng.Intn(4)})
		for limited.Status == Unknown {
			limited, _ = Resume(a, b, Config{Alphabet: alphabet, MaxLen: maxLen, Budget: 1 + rng.Intn(4)}, limited.Frontier)
		}
		if limited.Status != v.Status {
			t.Fatalf("trial %d: resumed=%v one-shot=%v", trial, limited.Status, v.Status)
		}
		if limited.Status == NotEqual && a.Run(limited.Witness) == b.Run(limited.Witness) {
			t.Fatalf("trial %d: resumed witness %q does not verify", trial, limited.Witness)
		}
	}
}
