package main

import (
	"reflect"
	"testing"
)

var abc = []string{"a", "b", "c"}

func TestDecideDepthAndDenyWins(t *testing.T) {
	r := rules{
		encode([]string{}):          true,  // allow .
		encode([]string{"a"}):       false, // deny a
		encode([]string{"a", "b"}):  true,  // allow a.b
		encode([]string{"c", "c"}):  true,  // allow c.c
	}
	cases := []struct {
		path []string
		want bool
	}{
		{[]string{"b"}, true},                   // only root matches
		{[]string{"a"}, false},                  // deny a
		{[]string{"a", "b"}, true},              // deeper allow a.b wins
		{[]string{"a", "c"}, false},             // inherits deny a
		{[]string{"c", "c", "c"}, true},         // inherits allow c.c
		{[]string{"c", "a"}, true},              // root only
	}
	for _, c := range cases {
		if got := r.decide(c.path, false); got != c.want {
			t.Errorf("decide(%v) = %v, want %v", c.path, got, c.want)
		}
	}
	// Same prefix allow+deny must resolve to deny.
	conflict := rules{encode([]string{"a"}): true}
	conflict[encode([]string{"a"})] = conflict[encode([]string{"a"})] && false
	if conflict.decide([]string{"a"}, true) {
		t.Error("same-depth allow+deny should deny")
	}
	// Default applies when nothing matches.
	if (rules{}).decide([]string{"a"}, false) {
		t.Error("empty rules with default deny should deny")
	}
	if !(rules{}).decide([]string{"a"}, true) {
		t.Error("empty rules with default allow should allow")
	}
}

func coverStrings(cover [][]string) []string {
	var out []string
	for _, p := range cover {
		out = append(out, formatPath(p))
	}
	return out
}

func TestMinimalCover(t *testing.T) {
	// Target: all leaves under "a" (depth 2), plus leaf b.a.
	target := map[string]bool{}
	enumPaths(abc, 1, func(suffix []string) {
		target[encode(append([]string{"a"}, suffix...))] = true
	})
	target[encode([]string{"b", "a"})] = true

	got := coverStrings(minimalCover(target, abc, 2))
	want := []string{"a", "b.a"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("minimalCover = %v, want %v", got, want)
	}
	if err := verifyCover("t", target, minimalCover(target, abc, 2), abc, 2); err != nil {
		t.Error(err)
	}

	// Full space collapses to root.
	all := map[string]bool{}
	enumPaths(abc, 2, func(p []string) { all[encode(p)] = true })
	got = coverStrings(minimalCover(all, abc, 2))
	if !reflect.DeepEqual(got, []string{"."}) {
		t.Errorf("full cover = %v, want [.]", got)
	}

	// Empty target yields empty cover.
	if c := minimalCover(map[string]bool{}, abc, 2); len(c) != 0 {
		t.Errorf("empty target cover = %v, want []", c)
	}
}

func TestVerifyCoverRejectsBadCovers(t *testing.T) {
	target := map[string]bool{
		encode([]string{"a", "a"}): true,
		encode([]string{"a", "b"}): true,
	}
	good := minimalCover(target, abc, 2)
	if err := verifyCover("ok", target, good, abc, 2); err != nil {
		t.Fatalf("good cover rejected: %v", err)
	}
	// Missing leaf.
	if err := verifyCover("gap", target, [][]string{{"a", "a"}}, abc, 2); err == nil {
		t.Error("expected uncovered-leaf error")
	}
	// Prefix covering leaves outside target.
	if err := verifyCover("wide", target, [][]string{{"a"}}, abc, 2); err == nil {
		t.Error("expected outside-target error")
	}
	// Overlapping prefixes.
	dup := [][]string{{"a", "a"}, {"a", "a"}}
	if err := verifyCover("dup", target, dup, abc, 2); err == nil {
		t.Error("expected overlap error")
	}
	// Redundant non-maximal prefix (parent "a" fully covered).
	full := map[string]bool{}
	enumPaths(abc, 1, func(s []string) { full[encode(append([]string{"a"}, s...))] = true })
	if err := verifyCover("redundant", full, [][]string{{"a", "a"}, {"a", "b"}, {"a", "c"}}, abc, 2); err == nil {
		t.Error("expected redundancy error")
	}
}

func TestEndToEndDiff(t *testing.T) {
	oldR := rules{encode([]string{}): true, encode([]string{"a"}): false}
	newR := rules{encode([]string{}): true, encode([]string{"b"}): false}

	expansion, shrinkage := map[string]bool{}, map[string]bool{}
	enumPaths(abc, 2, func(p []string) {
		was, now := oldR.decide(p, false), newR.decide(p, false)
		switch {
		case !was && now:
			expansion[encode(p)] = true
		case was && !now:
			shrinkage[encode(p)] = true
		}
	})
	if got := coverStrings(minimalCover(expansion, abc, 2)); !reflect.DeepEqual(got, []string{"a"}) {
		t.Errorf("expansion = %v, want [a]", got)
	}
	if got := coverStrings(minimalCover(shrinkage, abc, 2)); !reflect.DeepEqual(got, []string{"b"}) {
		t.Errorf("shrinkage = %v, want [b]", got)
	}
	if err := verifyCover("expansion", expansion, minimalCover(expansion, abc, 2), abc, 2); err != nil {
		t.Error(err)
	}
	if err := verifyCover("shrinkage", shrinkage, minimalCover(shrinkage, abc, 2), abc, 2); err != nil {
		t.Error(err)
	}
}
