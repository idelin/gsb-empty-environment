package main

import (
	"fmt"
	"reflect"
	"testing"
)

func file(p string, v int64, d string) Entry {
	return Entry{Path: p, Type: TypeFile, Version: v, Digest: d}
}
func dir(p string, v int64) Entry  { return Entry{Path: p, Type: TypeDir, Version: v} }
func tomb(p string, v int64) Entry { return Entry{Path: p, Type: TypeTombstone, Version: v} }

func src(name string, entries ...Entry) Source {
	s := Source{Name: name, Entries: entries}
	if err := validateSource(&s); err != nil {
		panic(err)
	}
	return s
}

func conflictKinds(r Result) []string {
	var kinds []string
	for _, c := range r.Conflicts {
		kinds = append(kinds, c.Kind)
	}
	return kinds
}

func hasConflict(r Result, kind string) bool {
	for _, c := range r.Conflicts {
		if c.Kind == kind {
			return true
		}
	}
	return false
}

func findEntry(r Result, p string) (ManifestEntry, bool) {
	for _, e := range r.Entries {
		if e.Path == p {
			return e, true
		}
	}
	return ManifestEntry{}, false
}

func TestNewerVersionWins(t *testing.T) {
	r := Merge([]Source{
		src("old", file("a.txt", 1, "d1")),
		src("new", file("a.txt", 2, "d2")),
	})
	if len(r.Conflicts) != 0 {
		t.Fatalf("unexpected conflicts: %v", r.Conflicts)
	}
	e, ok := findEntry(r, "a.txt")
	if !ok || e.Version != 2 || e.Digest != "d2" {
		t.Fatalf("want v2/d2, got %+v", e)
	}
	if !reflect.DeepEqual(e.Sources, []string{"new"}) {
		t.Fatalf("want provenance [new], got %v", e.Sources)
	}
}

func TestSameVersionSameDigestMerges(t *testing.T) {
	r := Merge([]Source{
		src("s1", file("a.txt", 3, "d1")),
		src("s2", file("a.txt", 3, "d1")),
	})
	if len(r.Conflicts) != 0 {
		t.Fatalf("unexpected conflicts: %v", r.Conflicts)
	}
	e, _ := findEntry(r, "a.txt")
	if !reflect.DeepEqual(e.Sources, []string{"s1", "s2"}) {
		t.Fatalf("want provenance [s1 s2], got %v", e.Sources)
	}
}

func TestSameVersionDifferentDigestConflicts(t *testing.T) {
	r := Merge([]Source{
		src("s1", file("a.txt", 3, "d1")),
		src("s2", file("a.txt", 3, "d2")),
	})
	if !hasConflict(r, "digest-mismatch") {
		t.Fatalf("want digest-mismatch, got %v", r.Conflicts)
	}
	if _, ok := findEntry(r, "a.txt"); ok {
		t.Fatal("conflicted path must not appear in entries")
	}
}

func TestSameVersionTypeMismatchConflicts(t *testing.T) {
	r := Merge([]Source{
		src("s1", dir("a", 2)),
		src("s2", file("a", 2, "d1")),
	})
	if !hasConflict(r, "type-mismatch") {
		t.Fatalf("want type-mismatch, got %v", r.Conflicts)
	}
}

func TestNewerVersionMayChangeType(t *testing.T) {
	// A dir replaced by a newer file is a versioned change, not a conflict.
	r := Merge([]Source{
		src("s1", dir("a", 1)),
		src("s2", file("a", 2, "d1")),
	})
	if len(r.Conflicts) != 0 {
		t.Fatalf("unexpected conflicts: %v", r.Conflicts)
	}
	e, _ := findEntry(r, "a")
	if e.Type != TypeFile || e.Version != 2 {
		t.Fatalf("want file v2, got %+v", e)
	}
}

func TestPrefixConflictFileWithChildren(t *testing.T) {
	r := Merge([]Source{
		src("s1", file("a", 5, "d1")),
		src("s2", file("a/b", 1, "d2")),
	})
	if !hasConflict(r, "prefix-conflict") {
		t.Fatalf("want prefix-conflict, got %v", r.Conflicts)
	}
}

func TestPrefixConflictFromTombstoneLoser(t *testing.T) {
	// Tombstone at "a" loses to a newer file; old child still conflicts.
	r := Merge([]Source{
		src("s1", tomb("a", 1), file("a/b", 1, "d1")),
		src("s2", file("a", 2, "d2")),
	})
	if !hasConflict(r, "prefix-conflict") {
		t.Fatalf("want prefix-conflict, got %v", r.Conflicts)
	}
}

func TestEmptyDirPreserved(t *testing.T) {
	r := Merge([]Source{
		src("s1", dir("empty", 1)),
		src("s2", file("other.txt", 1, "d1")),
	})
	if len(r.Conflicts) != 0 {
		t.Fatalf("unexpected conflicts: %v", r.Conflicts)
	}
	e, ok := findEntry(r, "empty")
	if !ok || e.Type != TypeDir {
		t.Fatalf("empty dir lost: %+v", r.Entries)
	}
}

func TestTombstoneDeletesSubtree(t *testing.T) {
	r := Merge([]Source{
		src("s1", dir("a", 1), file("a/b.txt", 1, "d1"), file("a/c.txt", 1, "d2")),
		src("s2", tomb("a", 5)),
	})
	if len(r.Conflicts) != 0 {
		t.Fatalf("unexpected conflicts: %v", r.Conflicts)
	}
	if len(r.Entries) != 1 || r.Entries[0].Type != TypeTombstone || r.Entries[0].Path != "a" {
		t.Fatalf("want lone tombstone at a, got %+v", r.Entries)
	}
}

func TestTombstoneDeletesOnlyOlderVersions(t *testing.T) {
	r := Merge([]Source{
		src("s1", file("gone.txt", 1, "d1")),
		src("s2", tomb("gone.txt", 2)),
		src("s3", file("kept.txt", 3, "d2")),
		src("s4", tomb("kept.txt", 2)),
	})
	if len(r.Conflicts) != 0 {
		t.Fatalf("unexpected conflicts: %v", r.Conflicts)
	}
	gone, ok := findEntry(r, "gone.txt")
	if !ok || gone.Type != TypeTombstone || gone.Version != 2 {
		t.Fatalf("gone.txt should be a v2 tombstone, got %+v", r.Entries)
	}
	e, ok := findEntry(r, "kept.txt")
	if !ok || e.Type != TypeFile || e.Version != 3 {
		t.Fatalf("kept.txt v3 should survive older tombstone, got %+v", r.Entries)
	}
}

func TestTombstoneWithNewerChildConflicts(t *testing.T) {
	r := Merge([]Source{
		src("s1", tomb("a", 2)),
		src("s2", file("a/b.txt", 3, "d1")),
	})
	if !hasConflict(r, "tombstone-conflict") {
		t.Fatalf("want tombstone-conflict, got %v", r.Conflicts)
	}
}

func TestProvenanceTracksAllWinningSources(t *testing.T) {
	r := Merge([]Source{
		src("s1", file("x", 1, "d1")),
		src("s2", file("x", 2, "d2")),
		src("s3", file("x", 2, "d2")),
	})
	e, _ := findEntry(r, "x")
	if !reflect.DeepEqual(e.Sources, []string{"s2", "s3"}) {
		t.Fatalf("want [s2 s3], got %v", e.Sources)
	}
}

func TestEmptyMerge(t *testing.T) {
	r := Merge(nil)
	if len(r.Entries) != 0 || len(r.Conflicts) != 0 {
		t.Fatalf("want empty result, got %+v", r)
	}
}

// --- Small-tree enumeration ---

// enumOptions returns the per-path entry choices: absent, or file/dir/
// tombstone at versions 1..2 (files with the given digests).
func enumOptions(digests []string) []*Entry {
	opts := []*Entry{nil}
	for v := int64(1); v <= 2; v++ {
		for _, d := range digests {
			opts = append(opts, &Entry{Type: TypeFile, Version: v, Digest: d})
		}
		opts = append(opts, &Entry{Type: TypeDir, Version: v})
		opts = append(opts, &Entry{Type: TypeTombstone, Version: v})
	}
	return opts
}

// enumSources builds every source over paths {"a", "a/b"} from opts.
func enumSources(opts []*Entry) []Source {
	var sources []Source
	for _, o1 := range opts {
		for _, o2 := range opts {
			s := Source{Name: fmt.Sprintf("s%d", len(sources))}
			for path, o := range map[string]*Entry{"a": o1, "a/b": o2} {
				if o != nil {
					e := *o
					e.Path = path
					s.Entries = append(s.Entries, e)
				}
			}
			if err := validateSource(&s); err != nil {
				panic(err)
			}
			sources = append(sources, s)
		}
	}
	return sources
}

// checkInvariants verifies properties every merge result must satisfy.
func checkInvariants(t *testing.T, sources []Source, r Result) {
	t.Helper()

	if got := Merge(sources); !reflect.DeepEqual(got, r) {
		t.Fatalf("merge not deterministic for %v", sourceNamesOf(sources))
	}

	for i := 1; i < len(r.Entries); i++ {
		if r.Entries[i-1].Path >= r.Entries[i].Path {
			t.Fatalf("entries not sorted: %v", r.Entries)
		}
	}

	isFile := make(map[string]bool)
	isTomb := make(map[string]bool)
	for _, e := range r.Entries {
		if e.Type == TypeFile {
			isFile[e.Path] = true
		}
		if e.Type == TypeTombstone {
			isTomb[e.Path] = true
		}
		if len(e.Sources) == 0 {
			t.Fatalf("entry %v has no provenance", e.Path)
		}
		for _, s := range sources {
			for _, se := range s.Entries {
				if se.Path != e.Path {
					continue
				}
				if se.Version > e.Version {
					t.Fatalf("entry %v v%d loses to %s v%d", e.Path, e.Version, s.Name, se.Version)
				}
			}
		}
		// Provenance: listed sources are exactly those holding the winner.
		for _, s := range sources {
			holds := false
			for _, se := range s.Entries {
				if se.Path == e.Path && se.Version == e.Version &&
					se.Type == e.Type && se.Digest == e.Digest {
					holds = true
				}
			}
			listed := false
			for _, n := range e.Sources {
				if n == s.Name {
					listed = true
				}
			}
			if holds != listed {
				t.Fatalf("provenance mismatch for %v vs source %s", e.Path, s.Name)
			}
		}
	}

	if len(r.Conflicts) > 0 {
		return // structural invariants only hold for clean merges
	}
	for _, e := range r.Entries {
		for _, a := range ancestors(e.Path) {
			if isFile[a] {
				t.Fatalf("file %v has descendant %v without conflict", a, e.Path)
			}
			if isTomb[a] {
				t.Fatalf("tombstone %v has descendant %v without conflict", a, e.Path)
			}
		}
	}
}

func sourceNamesOf(sources []Source) []string {
	var names []string
	for _, s := range sources {
		names = append(names, s.Name)
	}
	return names
}

// TestEnumPairsOrderIndependent exhaustively checks all ordered pairs of
// small sources: merge(A,B) == merge(B,A), plus result invariants.
func TestEnumPairsOrderIndependent(t *testing.T) {
	sources := enumSources(enumOptions([]string{"d1", "d2"}))
	for _, a := range sources {
		for _, b := range sources {
			ab := Merge([]Source{a, b})
			ba := Merge([]Source{b, a})
			if !reflect.DeepEqual(ab, ba) {
				t.Fatalf("order matters: merge(%s,%s) != merge(%s,%s)",
					a.Name, b.Name, b.Name, a.Name)
			}
			checkInvariants(t, []Source{a, b}, ab)
		}
	}
}

// TestEnumTriplesOrderIndependent exhaustively checks all triples (with a
// single digest to keep the space small): all rotations agree.
func TestEnumTriplesOrderIndependent(t *testing.T) {
	sources := enumSources(enumOptions([]string{"d1"}))
	for _, a := range sources {
		for _, b := range sources {
			for _, c := range sources {
				abc := Merge([]Source{a, b, c})
				bca := Merge([]Source{b, c, a})
				cab := Merge([]Source{c, a, b})
				if !reflect.DeepEqual(abc, bca) || !reflect.DeepEqual(abc, cab) {
					t.Fatalf("order matters for %s,%s,%s", a.Name, b.Name, c.Name)
				}
				checkInvariants(t, []Source{a, b, c}, abc)
			}
		}
	}
}

// TestEnumCoversRequiredCases proves the enumeration actually exercises
// prefix conflicts, empty dirs, and tombstones.
func TestEnumCoversRequiredCases(t *testing.T) {
	sources := enumSources(enumOptions([]string{"d1", "d2"}))
	seen := map[string]bool{}
	emptyDirSeen := false
	for _, a := range sources {
		for _, b := range sources {
			r := Merge([]Source{a, b})
			for _, k := range conflictKinds(r) {
				seen[k] = true
			}
			for _, e := range r.Entries {
				if e.Type == TypeDir {
					hasChild := false
					for _, o := range r.Entries {
						if isUnder(e.Path, o.Path) {
							hasChild = true
						}
					}
					if !hasChild {
						emptyDirSeen = true
					}
				}
				if e.Type == TypeTombstone {
					seen["tombstone-emitted"] = true
				}
			}
		}
	}
	for _, want := range []string{
		"prefix-conflict", "tombstone-conflict", "type-mismatch",
		"digest-mismatch", "tombstone-emitted",
	} {
		if !seen[want] {
			t.Fatalf("enumeration never produced %s", want)
		}
	}
	if !emptyDirSeen {
		t.Fatal("enumeration never produced an empty dir")
	}
}
