package main

import (
	"reflect"
	"testing"
)

func TestFullyShadowedByUnion(t *testing.T) {
	cfg := &Config{
		Subjects: []string{"a", "b"},
		Actions:  []string{"read"},
		Segments: []string{"x"},
		MaxDepth: 1,
		Rules: []RuleSpec{
			{Effect: "allow", Subjects: []string{"a"}, Actions: []string{"*"}, Path: "**"},
			{Effect: "allow", Subjects: []string{"b"}, Actions: []string{"*"}, Path: "**"},
			{Effect: "deny", Subjects: []string{"*"}, Actions: []string{"*"}, Path: "**"},
		},
	}
	reports, err := Analyze(cfg)
	if err != nil {
		t.Fatal(err)
	}
	rep := reports[2]
	if rep.Status != StatusFullyShadowed {
		t.Fatalf("rule 2 status = %s, want fully-shadowed", rep.Status)
	}
	// No single earlier rule covers rule 2; the union of rules 0 and 1 does.
	if !reflect.DeepEqual(rep.CoveredBy, []int{0, 1}) {
		t.Fatalf("coveredBy = %v, want [0 1]", rep.CoveredBy)
	}
	if reports[0].Status != StatusEffective || reports[1].Status != StatusEffective {
		t.Fatalf("rules 0,1 should be effective, got %s, %s", reports[0].Status, reports[1].Status)
	}
}

func TestPartiallyShadowedWitness(t *testing.T) {
	cfg := &Config{
		Subjects: []string{"alice"},
		Actions:  []string{"read", "write"},
		Segments: []string{"docs", "etc"},
		MaxDepth: 2,
		Rules: []RuleSpec{
			{Effect: "allow", Subjects: []string{"alice"}, Actions: []string{"read"}, Path: "docs/*"},
			{Effect: "allow", Subjects: []string{"alice"}, Actions: []string{"*"}, Path: "docs/*"},
		},
	}
	reports, err := Analyze(cfg)
	if err != nil {
		t.Fatal(err)
	}
	rep := reports[1]
	if rep.Status != StatusPartiallyShadowed {
		t.Fatalf("rule 1 status = %s, want partially-shadowed", rep.Status)
	}
	if rep.Witness == nil {
		t.Fatal("missing witness")
	}
	// Witness must match rule 1 and no earlier rule.
	rules, err := compileRules(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if !rules[1].matches(*rep.Witness) {
		t.Fatalf("witness %v does not match rule 1", rep.Witness)
	}
	if rules[0].matches(*rep.Witness) {
		t.Fatalf("witness %v matches earlier rule 0", rep.Witness)
	}
	if rep.Witness.Action != "write" {
		t.Fatalf("witness action = %s, want write", rep.Witness.Action)
	}
}

func TestFullyShadowedBySingleRule(t *testing.T) {
	cfg := &Config{
		Subjects: []string{"alice", "bob"},
		Actions:  []string{"read", "write"},
		Segments: []string{"tmp", "secret"},
		MaxDepth: 2,
		Rules: []RuleSpec{
			{Effect: "deny", Subjects: []string{"*"}, Actions: []string{"write"}, Path: "tmp/*"},
			{Effect: "allow", Subjects: []string{"bob"}, Actions: []string{"write"}, Path: "tmp/secret"},
		},
	}
	reports, err := Analyze(cfg)
	if err != nil {
		t.Fatal(err)
	}
	rep := reports[1]
	if rep.Status != StatusFullyShadowed {
		t.Fatalf("rule 1 status = %s, want fully-shadowed", rep.Status)
	}
	if !reflect.DeepEqual(rep.CoveredBy, []int{0}) {
		t.Fatalf("coveredBy = %v, want [0]", rep.CoveredBy)
	}
}

func TestEmptyRule(t *testing.T) {
	cfg := &Config{
		Subjects: []string{"alice"},
		Actions:  []string{"read"},
		Segments: []string{"x"},
		MaxDepth: 1,
		Rules: []RuleSpec{
			{Effect: "allow", Subjects: []string{}, Actions: []string{"read"}, Path: "**"},
		},
	}
	reports, err := Analyze(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if reports[0].Status != StatusEmpty {
		t.Fatalf("status = %s, want empty", reports[0].Status)
	}
}

func TestMinCoverPicksMinimum(t *testing.T) {
	// Universe of 4 requests; candidates: {0,1}, {2,3}, {0,2}, {1,2,3} (rule 3 redundant).
	mk := func(idxs ...int) *bitset {
		b := newBitset(4)
		for _, i := range idxs {
			b.set(i)
		}
		return b
	}
	universe := mk(0, 1, 2, 3)
	prior := []candSet{
		{bits: mk(0, 1), rule: 0},
		{bits: mk(2, 3), rule: 1},
		{bits: mk(0, 2), rule: 2},
		{bits: mk(1, 2, 3), rule: 3},
	}
	got := minCover(universe, prior)
	if len(got) != 2 {
		t.Fatalf("minCover size = %d (%v), want 2", len(got), got)
	}
	covered := newBitset(4)
	for _, r := range got {
		covered.or(prior[r].bits)
	}
	if covered.andNot(universe).count() != 0 || universe.andNot(covered).count() != 0 {
		t.Fatalf("cover %v does not equal universe", got)
	}
}

func TestPatternMatching(t *testing.T) {
	cases := []struct {
		pattern string
		path    []string
		want    bool
	}{
		{"", nil, true},
		{"", []string{"a"}, false},
		{"a", []string{"a"}, true},
		{"a", []string{"b"}, false},
		{"a/*", []string{"a", "b"}, true},
		{"a/*", []string{"a"}, false},
		{"a/*", []string{"a", "b", "c"}, false},
		{"a/**", []string{"a"}, true},
		{"a/**", []string{"a", "b", "c"}, true},
		{"a/**", []string{"b"}, false},
		{"**", nil, true},
		{"**", []string{"a", "b"}, true},
		{"*/b", []string{"a", "b"}, true},
		{"*/b", []string{"b"}, false},
	}
	for _, c := range cases {
		p, err := parsePattern(c.pattern)
		if err != nil {
			t.Fatalf("parsePattern(%q): %v", c.pattern, err)
		}
		if got := p.matches(c.path); got != c.want {
			t.Errorf("pattern %q vs path %v: got %v, want %v", c.pattern, c.path, got, c.want)
		}
	}
}

func TestPatternErrors(t *testing.T) {
	for _, bad := range []string{"a/**/b", "a*b", "a//b"} {
		if _, err := parsePattern(bad); err == nil {
			t.Errorf("parsePattern(%q) should fail", bad)
		}
	}
}

func TestUnknownSubject(t *testing.T) {
	cfg := &Config{
		Subjects: []string{"alice"},
		Actions:  []string{"read"},
		Segments: []string{"x"},
		MaxDepth: 1,
		Rules: []RuleSpec{
			{Effect: "allow", Subjects: []string{"mallory"}, Actions: []string{"read"}, Path: "**"},
		},
	}
	if _, err := Analyze(cfg); err == nil {
		t.Fatal("expected error for unknown subject")
	}
}

func TestDepthLimitRespected(t *testing.T) {
	cfg := &Config{
		Subjects: []string{"alice"},
		Actions:  []string{"read"},
		Segments: []string{"a"},
		MaxDepth: 1,
		Rules: []RuleSpec{
			{Effect: "deny", Subjects: []string{"*"}, Actions: []string{"*"}, Path: "a/a"},
			{Effect: "allow", Subjects: []string{"*"}, Actions: []string{"*"}, Path: "**"},
		},
	}
	reports, err := Analyze(cfg)
	if err != nil {
		t.Fatal(err)
	}
	// "a/a" is deeper than maxDepth=1, so rule 0 matches nothing.
	if reports[0].Status != StatusEmpty {
		t.Fatalf("rule 0 status = %s, want empty", reports[0].Status)
	}
	if reports[1].Status != StatusEffective {
		t.Fatalf("rule 1 status = %s, want effective", reports[1].Status)
	}
}
