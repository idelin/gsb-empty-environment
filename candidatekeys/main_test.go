package main

import (
	"reflect"
	"strings"
	"testing"
)

func keysOf(t *testing.T, attrs string, fdLines ...string) (*schema, []uint32) {
	t.Helper()
	var raw [][2]string
	for _, l := range fdLines {
		parts := strings.SplitN(l, "->", 2)
		if len(parts) != 2 {
			t.Fatalf("bad test FD %q", l)
		}
		raw = append(raw, [2]string{parts[0], parts[1]})
	}
	s, err := buildSchema(splitNames(attrs), raw)
	if err != nil {
		t.Fatal(err)
	}
	return s, s.candidateKeys()
}

func keyStrings(s *schema, keys []uint32) []string {
	var out []string
	for _, k := range keys {
		out = append(out, s.formatSet(k))
	}
	return out
}

// Cyclic dependencies A <-> B must not cause keys containing either side of
// the cycle to be missed: both {A,D} and {B,D} are candidate keys.
func TestCyclicDependencies(t *testing.T) {
	s, keys := keysOf(t, "", "A -> B", "B -> A", "A D -> C", "B D -> C")
	got := keyStrings(s, keys)
	want := []string{"{A, D}", "{B, D}"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

// A pure cycle with no outside determinants: every singleton is a key.
func TestPureCycle(t *testing.T) {
	s, keys := keysOf(t, "", "A -> B", "B -> C", "C -> A")
	got := keyStrings(s, keys)
	want := []string{"{A}", "{B}", "{C}"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

// Empty LHS FDs derive everything: the empty set is the only candidate key.
func TestEmptyKey(t *testing.T) {
	s, keys := keysOf(t, "", "-> A", "A -> B")
	got := keyStrings(s, keys)
	want := []string{"{}"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	if s.closure(0) != s.full {
		t.Fatalf("empty closure %v != full %v", s.formatSet(s.closure(0)), s.formatSet(s.full))
	}
}

// Attributes declared only via -attrs still work with the empty key.
func TestEmptyKeyWithDeclaredAttrs(t *testing.T) {
	s, keys := keysOf(t, "A, B", "-> A", "A -> B")
	if got := keyStrings(s, keys); !reflect.DeepEqual(got, []string{"{}"}) {
		t.Fatalf("got %v", got)
	}
}

// Classic textbook example with ordering by size then lexicographically.
func TestOrderingAndMinimality(t *testing.T) {
	s, keys := keysOf(t, "", "A B -> C", "C -> D", "D -> A", "C -> E")
	got := keyStrings(s, keys)
	// {C,B} and {D,B} are size-2 keys; {A,B} too. Lexicographic within size.
	want := []string{"{A, B}", "{B, C}", "{B, D}"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

// No FDs at all: the only key is the full attribute set.
func TestNoFDs(t *testing.T) {
	s, keys := keysOf(t, "A, B, C")
	got := keyStrings(s, keys)
	want := []string{"{A, B, C}"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

// Every member of a minimal key must lose at least one attribute when removed.
func TestRemovalMissingNonEmpty(t *testing.T) {
	s, keys := keysOf(t, "", "A B -> C", "C -> D", "D -> A", "C -> E")
	for _, k := range keys {
		for bit := uint(0); bit < uint(len(s.names)); bit++ {
			m := uint32(1) << bit
			if k&m == 0 {
				continue
			}
			missing := s.full &^ s.closure(k &^ m)
			if missing == 0 {
				t.Fatalf("key %v member %v: missing set is empty", s.formatSet(k), s.names[bit])
			}
		}
	}
}

// 24 attributes are accepted; 25 are rejected.
func TestAttributeLimit(t *testing.T) {
	names := make([]string, 24)
	for i := range names {
		names[i] = string(rune('A' + i))
	}
	if _, err := buildSchema(names, nil); err != nil {
		t.Fatalf("24 attrs rejected: %v", err)
	}
	if _, err := buildSchema(append(names, "Z2"), nil); err == nil {
		t.Fatal("25 attrs accepted")
	}
}

// forEachCombo must enumerate C(n,k) masks in lexicographic order.
func TestForEachCombo(t *testing.T) {
	var got []uint32
	forEachCombo(4, 2, func(m uint32) { got = append(got, m) })
	want := []uint32{3, 5, 9, 6, 10, 12}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %b, want %b", got, want)
	}
	count := 0
	forEachCombo(24, 12, func(m uint32) { count++ })
	if count != 2704156 {
		t.Fatalf("C(24,12) = %d", count)
	}
}
