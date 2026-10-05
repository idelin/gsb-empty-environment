package main

import (
	"strings"
	"testing"
)

func TestSplitCommand(t *testing.T) {
	argv, err := splitCommand(`run --flag "two words" 'x y' a\b`)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"run", "--flag", "two words", "x y", "ab"}
	if strings.Join(argv, "|") != strings.Join(want, "|") {
		t.Fatalf("got %q want %q", argv, want)
	}
	if _, err := splitCommand(`"unterminated`); err == nil {
		t.Fatal("expected error for unterminated quote")
	}
}

func TestParseRules(t *testing.T) {
	rules, err := parseRules("trim,collapse,float-strict", 1e-6, []string{`t=\d+`})
	if err != nil {
		t.Fatal(err)
	}
	if !rules.TrimSpace || !rules.CollapseSpace || !rules.FloatMode || !rules.FloatStrict {
		t.Fatalf("rules not parsed: %+v", rules)
	}
	if _, err := parseRules("bogus", 0, nil); err == nil {
		t.Fatal("expected error for unknown rule")
	}
}
