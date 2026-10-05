package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func file(p string, v int64, d string) Entry { return Entry{p, TypeFile, v, d} }
func dirE(p string, v int64) Entry           { return Entry{p, TypeDir, v, ""} }
func tomb(p string, v int64) Entry           { return Entry{p, TypeTombstone, v, ""} }

func src(name string, es ...Entry) Source { return Source{Name: name, Entries: es} }

func canonical(t *testing.T, r Result) string {
	t.Helper()
	b, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func isStrictDescendant(p, ancestor string) bool {
	return strings.HasPrefix(p, ancestor+"/")
}

func entryPaths(r Result) map[string]ManifestEntry {
	m := make(map[string]ManifestEntry, len(r.Entries))
	for _, e := range r.Entries {
		m[e.Path] = e
	}
	return m
}

// checkInvariants verifies structural properties every merge result must
// satisfy, regardless of input order.
func checkInvariants(t *testing.T, r Result, sources []Source) {
	t.Helper()
	entries := entryPaths(r)

	// Entries sorted by path, no duplicates.
	for i := 1; i < len(r.Entries); i++ {
		if r.Entries[i-1].Path >= r.Entries[i].Path {
			t.Fatalf("entries not sorted/unique: %q then %q", r.Entries[i-1].Path, r.Entries[i].Path)
		}
	}

	// No entry may live under a file entry or a deleted path.
	for _, e := range r.Entries {
		for _, f := range r.Entries {
			if f.Type == TypeFile && isStrictDescendant(e.Path, f.Path) {
				t.Fatalf("entry %q lives under file %q", e.Path, f.Path)
			}
		}
		for _, d := range r.Deleted {
			if isStrictDescendant(e.Path, d.Path) {
				t.Fatalf("entry %q lives under deleted %q", e.Path, d.Path)
			}
		}
	}

	// Conflicted paths never appear in the manifest.
	for _, c := range r.Conflicts {
		if _, ok := entries[c.Path]; ok {
			t.Fatalf("conflicted path %q present in entries", c.Path)
		}
		switch c.Reason {
		case ReasonDigestMismatch:
			dig := make(map[string]bool)
			for _, cand := range c.Candidates {
				if cand.Type != TypeFile {
					t.Fatalf("digest-mismatch at %q involves %s", c.Path, cand.Type)
				}
				dig[cand.Digest] = true
			}
			if len(dig) < 2 {
				t.Fatalf("digest-mismatch at %q with identical digests", c.Path)
			}
		case ReasonTypeMismatch, ReasonTombstoneVsContent:
		default:
			t.Fatalf("unknown conflict reason %q", c.Reason)
		}
	}

	// Suppression is complete: any source entry strictly under a winning
	// file/tombstone path must show up in Suppressed with that blocker.
	blockers := make(map[string]EntryType)
	for _, e := range r.Entries {
		if e.Type == TypeFile {
			blockers[e.Path] = TypeFile
		}
	}
	for _, d := range r.Deleted {
		blockers[d.Path] = TypeTombstone
	}
	type suppKey struct{ path, source string }
	supp := make(map[suppKey]Suppressed)
	for _, s := range r.Suppressed {
		supp[suppKey{s.Path, s.Source}] = s
		if !isStrictDescendant(s.Path, s.BlockedBy) {
			t.Fatalf("suppressed %q not under blocker %q", s.Path, s.BlockedBy)
		}
		if _, ok := blockers[s.BlockedBy]; !ok {
			t.Fatalf("suppressed %q cites non-blocker %q", s.Path, s.BlockedBy)
		}
	}
	for _, s := range sources {
		for _, e := range s.Entries {
			p := normPath(e.Path)
			for b := range blockers {
				if isStrictDescendant(p, b) {
					if _, ok := supp[suppKey{p, s.Name}]; !ok {
						t.Fatalf("entry %q from %s under blocker %q not suppressed", p, s.Name, b)
					}
				}
			}
		}
	}

	// Provenance sanity: every manifest entry names at least one source,
	// and all named sources were inputs.
	names := make(map[string]bool)
	for _, s := range sources {
		names[s.Name] = true
	}
	for _, e := range r.Entries {
		if len(e.Sources) == 0 {
			t.Fatalf("entry %q has no provenance", e.Path)
		}
		for _, p := range append(append([]Provenance{}, e.Sources...), e.Superseded...) {
			if !names[p.Source] {
				t.Fatalf("entry %q cites unknown source %q", e.Path, p.Source)
			}
		}
	}
}

// pathOptions enumerates the per-path possibilities for a small tree:
// absent, file (2 versions x 2 digests), dir (2 versions), tombstone (2 versions).
func pathOptions(p string) [][]Entry {
	return [][]Entry{
		{},
		{file(p, 1, "x")}, {file(p, 1, "y")},
		{file(p, 2, "x")}, {file(p, 2, "y")},
		{dirE(p, 1)}, {dirE(p, 2)},
		{tomb(p, 1)}, {tomb(p, 2)},
	}
}

// enumerateSources builds every small tree over the given paths.
func enumerateSources(paths []string) []Source {
	var sources []Source
	idx := 0
	var rec func(i int, acc []Entry)
	rec = func(i int, acc []Entry) {
		if i == len(paths) {
			cp := append([]Entry(nil), acc...)
			sources = append(sources, src(fmt.Sprintf("s%03d", idx), cp...))
			idx++
			return
		}
		for _, opt := range pathOptions(paths[i]) {
			rec(i+1, append(acc, opt...))
		}
	}
	rec(0, nil)
	return sources
}

// TestEnumerationPairs exhaustively merges all pairs of small trees over
// {"a", "a/b"} (81 trees, 6561 pairs) and checks order independence,
// idempotence, prefix blocking, tombstones and structural invariants.
func TestEnumerationPairs(t *testing.T) {
	sources := enumerateSources([]string{"a", "a/b"})
	for _, a := range sources {
		// Idempotence: merging a tree with itself changes nothing.
		r1 := Merge([]Source{a})
		r2 := Merge([]Source{a, a})
		if canonical(t, r1) != canonical(t, r2) {
			t.Fatalf("idempotence violated for %v", a.Entries)
		}
		if len(r2.Conflicts) != 0 {
			t.Fatalf("self-merge conflict for %v", a.Entries)
		}
		checkInvariants(t, r1, []Source{a})
	}
	for _, a := range sources {
		for _, b := range sources {
			rAB := Merge([]Source{a, b})
			rBA := Merge([]Source{b, a})
			if canonical(t, rAB) != canonical(t, rBA) {
				t.Fatalf("order dependence:\n%v\n%v\n%s\n%s",
					a.Entries, b.Entries, canonical(t, rAB), canonical(t, rBA))
			}
			checkInvariants(t, rAB, []Source{a, b})
		}
	}
}

// TestEnumerationTriplePermutations samples triples of small trees and
// checks that all 6 input permutations produce identical results.
func TestEnumerationTriplePermutations(t *testing.T) {
	sources := enumerateSources([]string{"a", "a/b"})
	n := len(sources)
	perm := [][]int{{0, 1, 2}, {0, 2, 1}, {1, 0, 2}, {1, 2, 0}, {2, 0, 1}, {2, 1, 0}}
	for k := 0; k < 3000; k++ {
		triple := []Source{
			sources[(k*7+1)%n],
			sources[(k*13+29)%n],
			sources[(k*31+5)%n],
		}
		var want string
		for pi, p := range perm {
			r := Merge([]Source{triple[p[0]], triple[p[1]], triple[p[2]]})
			got := canonical(t, r)
			if pi == 0 {
				want = got
				checkInvariants(t, r, triple)
			} else if got != want {
				t.Fatalf("permutation %v of triple %d differs", p, k)
			}
		}
	}
}

func TestEmptyDirPreserved(t *testing.T) {
	r := Merge([]Source{
		src("s1", dirE("empty", 1), dirE("d", 1), file("d/f", 1, "x")),
		src("s2", file("unrelated", 0, "y")),
	})
	entries := entryPaths(r)
	if e, ok := entries["empty"]; !ok || e.Type != TypeDir {
		t.Fatalf("empty dir lost: %+v", r.Entries)
	}
	if entries["d"].Type != TypeDir || entries["d/f"].Type != TypeFile {
		t.Fatalf("dir tree broken: %+v", r.Entries)
	}
	// Empty dir in one source vs same dir (any version) in another: still a dir.
	r = Merge([]Source{src("a", dirE("d", 1)), src("b", dirE("d", 2))})
	if len(r.Entries) != 1 || r.Entries[0].Type != TypeDir || r.Entries[0].Version != 2 {
		t.Fatalf("dir merge wrong: %+v", r.Entries)
	}
	if len(r.Entries[0].Sources) != 1 || r.Entries[0].Sources[0].Source != "b" {
		t.Fatalf("dir provenance wrong: %+v", r.Entries[0].Sources)
	}
}

func TestPrefixConflictFileBlocksSubtree(t *testing.T) {
	// File at "a" (v2) beats dir at "a" (v1): the whole subtree under "a"
	// from the other source is suppressed, even a higher-versioned file.
	r := Merge([]Source{
		src("s1", file("a", 2, "x")),
		src("s2", dirE("a", 1), file("a/c", 9, "y")),
	})
	entries := entryPaths(r)
	if e, ok := entries["a"]; !ok || e.Type != TypeFile {
		t.Fatalf("file should win at a: %+v", r.Entries)
	}
	if _, ok := entries["a/c"]; ok {
		t.Fatalf("a/c must be suppressed: %+v", r.Entries)
	}
	found := false
	for _, s := range r.Suppressed {
		if s.Path == "a/c" && s.Source == "s2" && s.Reason == SuppUnderFile && s.BlockedBy == "a" {
			found = true
		}
	}
	if !found {
		t.Fatalf("missing suppression record: %+v", r.Suppressed)
	}
	// The losing dir at "a" is recorded as superseded, not suppressed.
	if len(entries["a"].Superseded) != 1 || entries["a"].Superseded[0].Source != "s2" {
		t.Fatalf("superseded wrong: %+v", entries["a"].Superseded)
	}
}

func TestDirWinsOverOlderFile(t *testing.T) {
	// Dir at "a" (v3) beats file at "a" (v1): subtree survives, file superseded.
	r := Merge([]Source{
		src("s1", dirE("a", 3), file("a/b", 1, "x")),
		src("s2", file("a", 1, "z")),
	})
	entries := entryPaths(r)
	if entries["a"].Type != TypeDir || entries["a/b"].Type != TypeFile {
		t.Fatalf("dir subtree should survive: %+v", r.Entries)
	}
	if len(r.Suppressed) != 0 {
		t.Fatalf("nothing should be suppressed: %+v", r.Suppressed)
	}
}

func TestTombstone(t *testing.T) {
	// Winning tombstone deletes the path and its subtree.
	r := Merge([]Source{
		src("s1", tomb("a", 3)),
		src("s2", dirE("a", 1), file("a/f", 9, "x")),
	})
	if len(r.Entries) != 0 {
		t.Fatalf("tombstone should remove everything: %+v", r.Entries)
	}
	if len(r.Deleted) != 1 || r.Deleted[0].Path != "a" || r.Deleted[0].Version != 3 {
		t.Fatalf("deletion record wrong: %+v", r.Deleted)
	}
	if len(r.Suppressed) != 1 || r.Suppressed[0].Path != "a/f" ||
		r.Suppressed[0].Reason != SuppUnderTombstone {
		t.Fatalf("subtree suppression wrong: %+v", r.Suppressed)
	}

	// Newer content beats an older tombstone.
	r = Merge([]Source{src("s1", tomb("a", 1)), src("s2", file("a", 2, "x"))})
	entries := entryPaths(r)
	if e, ok := entries["a"]; !ok || e.Type != TypeFile || e.Version != 2 {
		t.Fatalf("newer file should beat tombstone: %+v", r.Entries)
	}
	if len(entries["a"].Superseded) != 1 {
		t.Fatalf("tombstone should be superseded: %+v", entries["a"].Superseded)
	}

	// Same version tombstone vs content is a conflict.
	r = Merge([]Source{src("s1", tomb("a", 2)), src("s2", file("a", 2, "x"))})
	if len(r.Conflicts) != 1 || r.Conflicts[0].Reason != ReasonTombstoneVsContent {
		t.Fatalf("expected tombstone-vs-content conflict: %+v", r.Conflicts)
	}
	if len(r.Entries) != 0 {
		t.Fatalf("conflicted path must not be emitted: %+v", r.Entries)
	}
}

func TestVersionAndDigestRules(t *testing.T) {
	// Higher version wins even with different digests: no conflict.
	r := Merge([]Source{src("s1", file("f", 2, "x")), src("s2", file("f", 1, "y"))})
	entries := entryPaths(r)
	if len(r.Conflicts) != 0 || entries["f"].Digest != "x" || entries["f"].Version != 2 {
		t.Fatalf("version should win: %+v %+v", r.Entries, r.Conflicts)
	}
	if len(entries["f"].Superseded) != 1 || entries["f"].Superseded[0].Source != "s2" {
		t.Fatalf("superseded wrong: %+v", entries["f"].Superseded)
	}

	// Same version, different digest: conflict, path excluded.
	r = Merge([]Source{src("s1", file("f", 1, "x")), src("s2", file("f", 1, "y"))})
	if len(r.Conflicts) != 1 || r.Conflicts[0].Reason != ReasonDigestMismatch {
		t.Fatalf("expected digest-mismatch: %+v", r.Conflicts)
	}
	if len(r.Entries) != 0 {
		t.Fatalf("conflicted path emitted: %+v", r.Entries)
	}

	// Same version, same digest: tie, both sources in provenance.
	r = Merge([]Source{src("s1", file("f", 1, "x")), src("s2", file("f", 1, "x"))})
	entries = entryPaths(r)
	if len(r.Conflicts) != 0 || len(entries["f"].Sources) != 2 {
		t.Fatalf("identical entries should tie: %+v %+v", r.Entries, r.Conflicts)
	}

	// Same version, file vs dir: conflict, never implicitly interchanged.
	r = Merge([]Source{src("s1", file("f", 1, "x")), src("s2", dirE("f", 1))})
	if len(r.Conflicts) != 1 || r.Conflicts[0].Reason != ReasonTypeMismatch {
		t.Fatalf("expected type-mismatch: %+v", r.Conflicts)
	}
}
