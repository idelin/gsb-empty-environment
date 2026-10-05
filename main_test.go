package main

import (
	"math/bits"
	"strings"
	"testing"
)

func keysFromInput(t *testing.T, input string) ([]uint32, []string) {
	t.Helper()
	names, fds, err := parse(strings.NewReader(input))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	full := uint32(1)<<uint(len(names)) - 1
	return enumerateKeys(len(names), fds, full), names
}

func keyStrings(keys []uint32, names []string) []string {
	var out []string
	for _, k := range keys {
		out = append(out, formatSet(k, names))
	}
	return out
}

func equalSets(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	seen := map[string]int{}
	for _, s := range a {
		seen[s]++
	}
	for _, s := range b {
		seen[s]--
		if seen[s] < 0 {
			return false
		}
	}
	return true
}

func TestCyclicDependencyKeys(t *testing.T) {
	keys, names := keysFromInput(t, "A -> B\nB -> A\nA -> C\n")
	want := []string{"{A}", "{B}"}
	if !equalSets(keyStrings(keys, names), want) {
		t.Fatalf("got %v, want %v", keyStrings(keys, names), want)
	}
}

func TestEmptyKey(t *testing.T) {
	keys, names := keysFromInput(t, "-> A, B\nA -> C\n")
	want := []string{"{}"}
	if !equalSets(keyStrings(keys, names), want) {
		t.Fatalf("got %v, want %v", keyStrings(keys, names), want)
	}
}

func TestNoFDs(t *testing.T) {
	// With no FDs the only candidate key is the full attribute set.
	names, fds, err := parse(strings.NewReader("A -> A\nB -> B\n"))
	if err != nil {
		t.Fatal(err)
	}
	full := uint32(1)<<uint(len(names)) - 1
	keys := enumerateKeys(len(names), fds, full)
	if len(keys) != 1 || keys[0] != full {
		t.Fatalf("got %v, want only the full set", keyStrings(keys, names))
	}
}

func TestClassic(t *testing.T) {
	keys, names := keysFromInput(t, "A, B -> C\nC -> B\n")
	want := []string{"{A, B}", "{A, C}"}
	if !equalSets(keyStrings(keys, names), want) {
		t.Fatalf("got %v, want %v", keyStrings(keys, names), want)
	}
}

func TestNonMinimalSupersetExcluded(t *testing.T) {
	// {B} alone is a key; {A,B} must not be reported even though the DFS
	// reaches it first along the A-branch.
	keys, names := keysFromInput(t, "B -> A\nB -> C\n")
	want := []string{"{B}"}
	if !equalSets(keyStrings(keys, names), want) {
		t.Fatalf("got %v, want %v", keyStrings(keys, names), want)
	}
}

func TestMinimalityCheck(t *testing.T) {
	names, fds, err := parse(strings.NewReader("A, B -> C\nC -> B\n"))
	if err != nil {
		t.Fatal(err)
	}
	full := uint32(1)<<uint(len(names)) - 1
	ab := uint32(0b011)
	if !isMinimal(ab, fds, full) {
		t.Fatal("{A,B} should be minimal")
	}
	abc := uint32(0b111)
	if isMinimal(abc, fds, full) {
		t.Fatal("{A,B,C} should not be minimal")
	}
}

func TestTooManyAttributes(t *testing.T) {
	var sb strings.Builder
	for i := 0; i < 25; i++ {
		sb.WriteString(string(rune('A'+i)) + " -> ZZ\n")
	}
	if _, _, err := parse(strings.NewReader(sb.String())); err == nil {
		t.Fatal("expected an error for more than 24 attributes")
	}
}

func TestKeyOrdering(t *testing.T) {
	names := []string{"A", "B", "C"}
	keys := []uint32{0b111, 0b010, 0b101, 0b001}
	// sort with keyLess
	for i := 0; i < len(keys); i++ {
		for j := i + 1; j < len(keys); j++ {
			if keyLess(keys[j], keys[i], names) {
				keys[i], keys[j] = keys[j], keys[i]
			}
		}
	}
	got := keyStrings(keys, names)
	want := []string{"{A}", "{B}", "{A, C}", "{A, B, C}"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
	if bits.OnesCount32(keys[0]) != 1 {
		t.Fatal("smallest key should come first")
	}
}
