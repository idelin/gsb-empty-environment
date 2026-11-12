package netsec

import (
	"math/rand/v2"
	"slices"
	"testing"
)

func orig(source string, line, col int) *OrigPos {
	return &OrigPos{Source: source, Line: line, Col: col}
}

func gen(line, col int) GenPos {
	return GenPos{Line: line, Col: col}
}

func entry(line, col int, src *OrigPos) Entry {
	return Entry{Gen: gen(line, col), Orig: src}
}

func TestNewMappingRejectsOutOfOrder(t *testing.T) {
	cases := [][]Entry{
		{entry(1, 0, orig("a", 0, 0)), entry(1, 0, orig("a", 0, 1))},
		{entry(2, 0, orig("a", 0, 0)), entry(1, 9, orig("a", 0, 1))},
	}
	for i, in := range cases {
		if _, err := NewMapping(in); err == nil {
			t.Fatalf("case %d: expected error for non-increasing generated positions", i)
		}
	}
}

func TestTrimDeterministic(t *testing.T) {
	// Layout:
	//   leading disallowed run, allowed block, gap, disallowed run, allowed
	//   block, then a trailing disallowed run. The disallowed sources are
	//   /build/secret.ts (absolute build-machine path) and unpriv/internal.ts.
	entries := []Entry{
		entry(0, 0, orig("/build/secret.ts", 1, 0)),
		entry(1, 0, orig("src/pub/a.ts", 10, 0)),
		entry(1, 20, orig("src/pub/a.ts", 10, 20)),
		entry(3, 0, orig("src/pub/b.ts", 20, 0)),
		entry(5, 0, orig("/build/secret.ts", 40, 0)),
		entry(5, 30, orig("unpriv/internal.ts", 7, 0)),
		entry(7, 0, orig("src/pub/a.ts", 30, 0)),
		entry(9, 0, orig("unpriv/internal.ts", 99, 0)),
	}
	m, err := NewMapping(entries)
	if err != nil {
		t.Fatalf("NewMapping: %v", err)
	}

	allowed := NewAllowedSources("src/pub/a.ts", "src/pub/b.ts")
	trimmed := m.Trim(allowed)

	// No published entry may reference a disallowed source.
	for _, e := range trimmed.Entries() {
		if e.Orig != nil && !allowed.Allows(e.Orig.Source) {
			t.Fatalf("trimmed mapping leaks disallowed source %q at %+v", e.Orig.Source, e.Gen)
		}
	}

	tests := []struct {
		name    string
		pos     GenPos
		wantNil bool
		wantSrc string
	}{
		{"at leading forbidden entry", gen(0, 0), true, ""},
		{"inside leading forbidden run", gen(0, 5), true, ""},
		{"allowed predecessor", gen(1, 5), false, "src/pub/a.ts"},
		{"still previous allowed entry", gen(1, 19), false, "src/pub/a.ts"},
		{"exact allowed entry", gen(1, 20), false, "src/pub/a.ts"},
		{"gap keeps predecessor semantics", gen(2, 999), false, "src/pub/a.ts"},
		{"next allowed file", gen(3, 40), false, "src/pub/b.ts"},
		{"forbidden run start is boundary", gen(5, 0), true, ""},
		{"forbidden run must not extend prior allowed entry", gen(5, 29), true, ""},
		{"inside multi-entry forbidden run", gen(6, 0), true, ""},
		{"allowed again after boundary", gen(7, 0), false, "src/pub/a.ts"},
		{"trailing forbidden run start", gen(9, 0), true, ""},
		{"past end stays inside trailing forbidden run", gen(99, 0), true, ""},
	}
	// A position before the first entry has no predecessor at all.
	if _, ok := trimmed.Lookup(gen(-1, 0)); ok {
		t.Fatal("position before first entry must report no predecessor")
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := trimmed.Lookup(tc.pos)
			if !ok {
				t.Fatalf("Lookup(%+v): no predecessor, expected one", tc.pos)
			}
			if (got.Orig == nil) != tc.wantNil {
				t.Fatalf("Lookup nil-orig = %v, want %v (entry %+v)", got.Orig == nil, tc.wantNil, got)
			}
			if got.Orig != nil && got.Orig.Source != tc.wantSrc {
				t.Fatalf("Lookup source = %q, want %q", got.Orig.Source, tc.wantSrc)
			}
		})
	}
}

func TestTrimEmptyAndAllDisallowed(t *testing.T) {
	empty, err := NewMapping(nil)
	if err != nil {
		t.Fatalf("NewMapping(nil): %v", err)
	}
	if _, ok := empty.Trim(NewAllowedSources("x")).Lookup(gen(0, 0)); ok {
		t.Fatal("empty mapping lookup must report no predecessor")
	}
	if got := empty.Trim(NewAllowedSources("x")); got.Len() != 0 {
		t.Fatalf("trimmed empty mapping has %d entries", got.Len())
	}

	allHidden, err := NewMapping([]Entry{
		entry(0, 0, orig("secret", 0, 0)),
		entry(1, 0, orig("secret", 1, 0)),
	})
	if err != nil {
		t.Fatalf("NewMapping: %v", err)
	}
	out := allHidden.Trim(NewAllowedSources("other"))
	if out.Len() != 1 {
		t.Fatalf("leading all-disallowed run must collapse to one boundary, got %d entries", out.Len())
	}
	got, ok := out.Lookup(gen(5, 0))
	if !ok || got.Orig != nil {
		t.Fatalf("fully disallowed mapping must resolve to a nil-original boundary, got ok=%v entry=%+v", ok, got)
	}
	if got.Gen != (gen(0, 0)) {
		t.Fatalf("boundary must sit at the run's first generated position, got %+v", got.Gen)
	}
}

func TestTrimKeepsNilBoundariesAndSwitchesFiles(t *testing.T) {
	entries := []Entry{
		entry(0, 0, orig("a.ts", 0, 0)),
		{Gen: gen(1, 0)}, // pre-existing generated-only boundary
		entry(2, 0, orig("b.ts", 0, 0)),
		entry(3, 0, orig("a.ts", 3, 0)),
		entry(4, 0, orig("b.ts", 4, 0)),
	}
	m, err := NewMapping(entries)
	if err != nil {
		t.Fatalf("NewMapping: %v", err)
	}

	// Only a.ts is allowed; b.ts entries alternate with it to exercise
	// multi-file switching with repeated forbidden runs.
	out := m.Trim(NewAllowedSources("a.ts"))

	wantSources := []struct {
		pos     GenPos
		nilOrig bool
		source  string
	}{
		{gen(0, 10), false, "a.ts"},
		{gen(1, 5), true, ""},  // explicit nil boundary preserved
		{gen(2, 0), true, ""},  // injected boundary blocks b.ts
		{gen(2, 99), true, ""}, // missing segment stays inside the forbidden run
		{gen(3, 0), false, "a.ts"},
		{gen(3, 99), false, "a.ts"},
		{gen(4, 0), true, ""}, // second switch back into forbidden b.ts
		{gen(99, 99), true, ""},
	}
	for _, tc := range wantSources {
		got, ok := out.Lookup(tc.pos)
		if !ok {
			t.Fatalf("Lookup(%+v): no predecessor, expected one", tc.pos)
		}
		if tc.nilOrig && got.Orig != nil {
			t.Fatalf("Lookup(%+v): expected nil-original boundary, got %+v", tc.pos, got.Orig)
		}
		if !tc.nilOrig && (got.Orig == nil || got.Orig.Source != tc.source) {
			src := "<nil>"
			if got.Orig != nil {
				src = got.Orig.Source
			}
			t.Fatalf("Lookup(%+v): source = %s, want %s", tc.pos, src, tc.source)
		}
	}
}

func TestPackageTrimAndZeroValueSources(t *testing.T) {
	m, err := NewMapping([]Entry{entry(0, 0, orig("a.ts", 0, 0))})
	if err != nil {
		t.Fatalf("NewMapping: %v", err)
	}
	var zero AllowedSources
	zero.Add("a.ts") // exercises the lazily-initialized nil map
	if !zero.Allows("a.ts") || zero.Allows("missing.ts") {
		t.Fatal("zero-value AllowedSources Add/Allows behavior mismatch")
	}

	out := Trim(m, &zero)
	got, ok := out.Lookup(gen(0, 0))
	if !ok || got.Orig == nil || got.Orig.Source != "a.ts" {
		t.Fatalf("package-level Trim returned unexpected result: ok=%v entry=%+v", ok, got)
	}
}

func TestTrimRandomDifferential(t *testing.T) {
	rng := rand.New(rand.NewPCG(0x9e3779b97f4a7c15, 0x14057b7ef767814f))
	sourcePool := []string{
		"src/allowed/a.ts",
		"src/allowed/b.ts",
		"/build/machine/secret.ts",
		"unpublished/internal.ts",
	}

	for iter := 0; iter < 400; iter++ {
		n := rng.IntN(60) + 1
		entries := make([]Entry, n)
		line, col := 0, 0
		for i := range entries {
			// Build strictly increasing generated positions, with occasional
			// large line jumps to create missing segments (gaps).
			if i > 0 {
				if rng.IntN(4) == 0 {
					line += rng.IntN(5) + 1
					col = rng.IntN(8)
				} else {
					col += rng.IntN(6) + 1
				}
			}
			if rng.IntN(8) == 0 {
				entries[i] = Entry{Gen: gen(line, col)} // generated-only entry
			} else {
				src := sourcePool[rng.IntN(len(sourcePool))]
				entries[i] = entry(line, col, orig(src, rng.IntN(100), rng.IntN(100)))
			}
		}
		m, err := NewMapping(entries)
		if err != nil {
			t.Fatalf("iter %d: NewMapping: %v", iter, err)
		}

		allowed := NewAllowedSources()
		for _, src := range sourcePool {
			if rng.IntN(2) == 0 {
				allowed.Add(src)
			}
		}
		out := m.Trim(allowed)

		// Invariant 1: every published original entry is allowed.
		for _, e := range out.Entries() {
			if e.Orig != nil && !allowed.Allows(e.Orig.Source) {
				t.Fatalf("iter %d: trimmed mapping leaks source %q", iter, e.Orig.Source)
			}
		}
		// Invariant 2: output remains strictly ordered and reconstructible.
		if _, err := NewMapping(out.Entries()); err != nil {
			t.Fatalf("iter %d: trimmed entries are not valid: %v", iter, err)
		}

		// Differential check: random queries must agree with the oracle derived
		// from the original mapping restricted to its allowed portion.
		for q := 0; q < 80; q++ {
			pos := randomQueryPos(rng, entries)
			oracleEntry, oracleOK := oracleLookup(entries, pos, allowed)
			gotEntry, gotOK := out.Lookup(pos)

			if gotOK != oracleOK {
				t.Fatalf("iter %d query %+v: ok = %v, oracle ok = %v", iter, pos, gotOK, oracleOK)
			}
			if !oracleOK {
				continue
			}
			if oracleEntry == nil {
				if gotEntry.Orig != nil {
					t.Fatalf("iter %d query %+v: expected no published original, got %+v", iter, pos, gotEntry.Orig)
				}
				continue
			}
			if gotEntry.Orig == nil || *gotEntry.Orig != *oracleEntry {
				got := "<nil>"
				if gotEntry.Orig != nil {
					got = gotEntry.Orig.Source
				}
				t.Fatalf("iter %d query %+v: got %s, want oracle %+v", iter, pos, got, oracleEntry)
			}
		}
	}
}

// randomQueryPos picks positions both inside the entry range and outside it
// (before the first entry and past the last), including gap positions.
func randomQueryPos(rng *rand.Rand, entries []Entry) GenPos {
	first, last := entries[0].Gen, entries[len(entries)-1].Gen
	switch rng.IntN(3) {
	case 0:
		return gen(max(first.Line-1, 0), rng.IntN(4))
	case 1:
		return gen(last.Line+rng.IntN(3), rng.IntN(10))
	default:
		line := first.Line + rng.IntN(max(last.Line-first.Line+1, 1))
		return gen(line, rng.IntN(40))
	}
}

// oracleLookup computes the expected published result for pos from the
// original, untrimmed entries: the predecessor entry's original location is
// published only when its source is allowed; a disallowed predecessor yields a
// nil-original boundary, and no predecessor yields ok == false.
func oracleLookup(entries []Entry, pos GenPos, allowed *AllowedSources) (*OrigPos, bool) {
	i, found := slices.BinarySearchFunc(entries, pos, func(e Entry, target GenPos) int {
		return CompareGenPos(e.Gen, target)
	})
	if found {
		i++
	}
	if i == 0 {
		return nil, false
	}
	pred := entries[i-1]
	if pred.Orig == nil || !allowed.Allows(pred.Orig.Source) {
		return nil, true
	}
	return pred.Orig, true
}
