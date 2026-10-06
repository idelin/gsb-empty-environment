package speckey

import (
	"testing"
)

func TestDedupSameKeySameContent(t *testing.T) {
	s := NewSet(ConflictError)
	k := NewKey(Str("fmt"), Str("1.0"))
	if o, err := s.Add(k, "content-a"); err != nil || o != Added {
		t.Fatalf("first add: got %v, %v", o, err)
	}
	for i := 0; i < 3; i++ {
		if o, err := s.Add(k, "content-a"); err != nil || o != Duplicate {
			t.Fatalf("re-add %d: got %v, %v, want Duplicate", i, o, err)
		}
	}
	if s.Len() != 1 {
		t.Fatalf("Len = %d, want 1", s.Len())
	}
	if c, ok := s.Get(k); !ok || c != "content-a" {
		t.Fatalf("Get = %q, %v", c, ok)
	}
}

func TestConflictErrorKeepsOriginal(t *testing.T) {
	s := NewSet(ConflictError)
	k := NewKey(Str("fmt"), Null())
	if _, err := s.Add(k, "v1"); err != nil {
		t.Fatal(err)
	}
	o, err := s.Add(k, "v2")
	if o != Rejected || err != ErrConflict {
		t.Fatalf("got %v, %v; want Rejected, ErrConflict", o, err)
	}
	if c, _ := s.Get(k); c != "v1" {
		t.Fatalf("original content overwritten: got %q", c)
	}
	if s.Len() != 1 {
		t.Fatalf("Len = %d, want 1", s.Len())
	}
}

func TestConflictIsolateKeepsBoth(t *testing.T) {
	s := NewSet(ConflictIsolate)
	k := NewKey(Str("fmt"), Str(""))
	mustAdd := func(content string, want Outcome) {
		t.Helper()
		if o, err := s.Add(k, content); err != nil || o != want {
			t.Fatalf("Add(%q): got %v, %v, want %v", content, o, err, want)
		}
	}
	mustAdd("v1", Added)
	mustAdd("v2", Isolated)
	mustAdd("v3", Isolated)
	mustAdd("v2", Duplicate) // isolated content dedups too
	mustAdd("v1", Duplicate) // primary still dedups

	if c, _ := s.Get(k); c != "v1" {
		t.Fatalf("primary content = %q, want v1", c)
	}
	all := s.GetAll(k)
	if len(all) != 3 {
		t.Fatalf("GetAll returned %v, want 3 contents", all)
	}
	seen := map[string]bool{}
	for _, c := range all {
		seen[c] = true
	}
	for _, want := range []string{"v1", "v2", "v3"} {
		if !seen[want] {
			t.Fatalf("content %q missing from GetAll %v", want, all)
		}
	}
	if s.Len() != 3 {
		t.Fatalf("Len = %d, want 3", s.Len())
	}
}

func TestConflictReplaceIsExplicit(t *testing.T) {
	s := NewSet(ConflictReplace)
	k := NewKey(Null(), Str("tag"))
	s.Add(k, "old")
	o, err := s.Add(k, "new")
	if o != Replaced || err != nil {
		t.Fatalf("got %v, %v, want Replaced", o, err)
	}
	if c, _ := s.Get(k); c != "new" {
		t.Fatalf("Get = %q, want new", c)
	}
	if s.Len() != 1 {
		t.Fatalf("Len = %d, want 1", s.Len())
	}
}

func TestDistinctKeysNeverConflict(t *testing.T) {
	s := NewSet(ConflictError)
	// Keys differing only in null-vs-empty or separator-looking content
	// are distinct keys, not conflicts.
	keys := []SpecKey{
		NewKey(Str("a"), Null()),
		NewKey(Str("a"), Str("")),
		NewKey(Str("a|"), Null()),
		NewKey(Str("a"), Str("|")),
		NewKey(Str("a"), Str("b")),
		NewKey(Str("ab")),
	}
	for i, k := range keys {
		if o, err := s.Add(k, "shared-content"); err != nil || o != Added {
			t.Fatalf("key %d: got %v, %v, want Added", i, o, err)
		}
	}
	if s.Len() != len(keys) {
		t.Fatalf("Len = %d, want %d", s.Len(), len(keys))
	}
}
