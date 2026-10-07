package main

import (
	"fmt"
	"math/rand"
	"reflect"
	"strings"
	"testing"
)

func mustParse(t *testing.T, text string) *Config {
	t.Helper()
	cfg, err := parseConfig(strings.NewReader(text))
	if err != nil {
		t.Fatalf("parseConfig: %v", err)
	}
	return cfg
}

func checkDiff(t *testing.T, cfg *Config, wantExpanded, wantShrunk []string) {
	t.Helper()
	expanded, shrunk := cfg.Diff()
	if !reflect.DeepEqual(expanded, wantExpanded) {
		t.Errorf("expanded = %q, want %q", expanded, wantExpanded)
	}
	if !reflect.DeepEqual(shrunk, wantShrunk) {
		t.Errorf("shrunk = %q, want %q", shrunk, wantShrunk)
	}
	if problems := cfg.Verify(expanded, shrunk); len(problems) != 0 {
		t.Errorf("verify reported problems:\n%s", strings.Join(problems, "\n"))
	}
}

func TestExpansionFromDefaultDeny(t *testing.T) {
	cfg := mustParse(t, `
alphabet ab
maxdepth 2
new allow a
`)
	checkDiff(t, cfg, []string{"a"}, nil)
}

func TestShrinkByDeeperDeny(t *testing.T) {
	cfg := mustParse(t, `
alphabet ab
maxdepth 2
old allow a
new allow a
new deny ab
`)
	checkDiff(t, cfg, nil, []string{"ab"})
}

func TestDenyWinsSameDepth(t *testing.T) {
	cfg := mustParse(t, `
alphabet ab
maxdepth 2
old allow a
old deny a
new allow a
`)
	// old resolves to deny (tie), new allows: the whole "a" subtree expands.
	checkDiff(t, cfg, []string{"a"}, nil)
}

func TestRemovedDeeperDenyExpands(t *testing.T) {
	cfg := mustParse(t, `
alphabet ab
maxdepth 3
old allow a
old deny ab
new allow a
`)
	// old denies "ab*" via the deeper rule; new drops it -> expansion.
	checkDiff(t, cfg, []string{"ab"}, nil)
}

func TestRootPrefixMatchesEverything(t *testing.T) {
	cfg := mustParse(t, `
alphabet ab
maxdepth 2
new allow -
`)
	checkDiff(t, cfg, []string{""}, nil)
}

func TestMixedRegions(t *testing.T) {
	cfg := mustParse(t, `
alphabet ab
maxdepth 2
old allow a
old deny b
new deny aa
new allow b
new deny bb
`)
	// "a" subtree: new has no matching rule -> default deny, all shrunk.
	// "b": deny->allow expands, but "bb" is denied again by the deeper rule.
	checkDiff(t, cfg, []string{"ba"}, []string{"a"})
}

func TestDefaultAllow(t *testing.T) {
	cfg := mustParse(t, `
alphabet ab
maxdepth 1
default allow
new deny a
`)
	checkDiff(t, cfg, nil, []string{"a"})
}

func TestNoChange(t *testing.T) {
	cfg := mustParse(t, `
alphabet ab
maxdepth 2
old allow a
new allow a
`)
	checkDiff(t, cfg, nil, nil)
}

func TestParseErrors(t *testing.T) {
	for _, text := range []string{
		"maxdepth 2\n",                            // missing alphabet
		"alphabet ab\n",                           // missing maxdepth
		"alphabet ab\nmaxdepth 0\n",               // bad depth
		"alphabet ab\nmaxdepth 2\nold allow ac\n", // symbol outside alphabet
		"alphabet ab\nmaxdepth 2\nold maybe a\n",  // bad action
		"alphabet ab\nmaxdepth 2\nbogus x\n",      // bad directive
		"alphabet ab\nmaxdepth 1\nold allow ab\n", // prefix deeper than maxdepth
	} {
		if _, err := parseConfig(strings.NewReader(text)); err == nil {
			t.Errorf("expected parse error for %q", text)
		}
	}
}

// TestRandomized cross-checks Diff against full path enumeration on many
// random instances, and confirms the emitted sets are exact, disjoint and
// non-redundant.
func TestRandomized(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	for trial := 0; trial < 500; trial++ {
		alphaSize := 2 + rng.Intn(2) // 2..3 symbols
		alphabet := "abc"[:alphaSize]
		maxDepth := 1 + rng.Intn(4) // 1..4

		var sb strings.Builder
		fmt.Fprintf(&sb, "alphabet %s\nmaxdepth %d\n", alphabet, maxDepth)
		if rng.Intn(4) == 0 {
			sb.WriteString("default allow\n")
		}
		for _, set := range []string{"old", "new"} {
			for i, n := 0, rng.Intn(7); i < n; i++ {
				action := "allow"
				if rng.Intn(2) == 0 {
					action = "deny"
				}
				plen := rng.Intn(maxDepth + 1)
				var p strings.Builder
				for j := 0; j < plen; j++ {
					p.WriteByte(alphabet[rng.Intn(alphaSize)])
				}
				prefix := p.String()
				if prefix == "" {
					prefix = "-"
				}
				fmt.Fprintf(&sb, "%s %s %s\n", set, action, prefix)
			}
		}

		cfg, err := parseConfig(strings.NewReader(sb.String()))
		if err != nil {
			t.Fatalf("trial %d: parse: %v\n%s", trial, err, sb.String())
		}
		expanded, shrunk := cfg.Diff()
		if problems := cfg.Verify(expanded, shrunk); len(problems) != 0 {
			t.Fatalf("trial %d: verify failed:\n%s\nconfig:\n%s", trial, strings.Join(problems, "\n"), sb.String())
		}
	}
}

// TestVerifyDetectsTampering ensures the verifier actually rejects wrong sets.
func TestVerifyDetectsTampering(t *testing.T) {
	cfg := mustParse(t, `
alphabet ab
maxdepth 2
old allow a
new allow a
new deny ab
`)
	expanded, shrunk := cfg.Diff()

	// Dropping the only shrunk prefix must break coverage.
	if problems := cfg.Verify(expanded, nil); len(problems) == 0 {
		t.Error("expected coverage problem after dropping a prefix")
	}
	// Adding an overlapping prefix must break disjointness.
	if problems := cfg.Verify(expanded, append(append([]string{}, shrunk...), "a")); len(problems) == 0 {
		t.Error("expected disjointness problem after adding overlapping prefix")
	}
	// Adding a redundant but disjoint prefix must be flagged.
	if problems := cfg.Verify(expanded, append(append([]string{}, shrunk...), "bb")); len(problems) == 0 {
		t.Error("expected problem after adding out-of-region prefix")
	}
}
