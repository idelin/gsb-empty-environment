package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func testTable(t *testing.T) *FoldTable {
	t.Helper()
	tb, err := LoadTable(strings.NewReader(`{
		"A":"a","B":"b","C":"c","D":"d","E":"e","F":"f","G":"g","H":"h","I":"i",
		"J":"j","K":"k","L":"l","M":"m","N":"n","O":"o","P":"p","Q":"q","R":"r",
		"S":"s","T":"t","U":"u","V":"v","W":"w","X":"x","Y":"y","Z":"z",
		"Ä":"ae","Ö":"oe","Ü":"ue","ß":"ss"
	}`))
	if err != nil {
		t.Fatal(err)
	}
	return tb
}

func findGroup(groups []Group, reason, norm string) *Group {
	for i := range groups {
		if groups[i].Reason == reason && groups[i].Norm == norm {
			return &groups[i]
		}
	}
	return nil
}

func hasOrigin(g *Group, path string, dir bool) bool {
	for _, o := range g.Origins {
		if o.Path == path && o.Dir == dir {
			return true
		}
	}
	return false
}

func TestFoldLongestMatch(t *testing.T) {
	tb, err := LoadTable(strings.NewReader(`{"ß":"ss","ßt":"X"}`))
	if err != nil {
		t.Fatal(err)
	}
	if got := tb.Fold("ßt"); got != "X" {
		t.Fatalf("longest match: got %q, want %q", got, "X")
	}
	if got := tb.Fold("ß"); got != "ss" {
		t.Fatalf("single key: got %q, want %q", got, "ss")
	}
}

func TestMultiSegmentFold(t *testing.T) {
	// Folding must apply independently to every path segment: "Ä/ß.txt"
	// and "ae/ss.txt" collide only if both segments are folded.
	groups := FindConflicts([]Entry{
		{Path: "Ä/ß.txt"},
		{Path: "ae/ss.txt"},
	}, testTable(t))
	if len(groups) != 1 {
		t.Fatalf("expected exactly 1 group, got %d: %+v", len(groups), groups)
	}
	g := groups[0]
	if g.Reason != "name" || g.Norm != "ae/ss.txt" {
		t.Fatalf("unexpected group: %+v", g)
	}
	// Original names must be preserved in the report, not folded ones.
	if !hasOrigin(&g, "Ä/ß.txt", false) || !hasOrigin(&g, "ae/ss.txt", false) {
		t.Fatalf("original names not preserved: %+v", g.Origins)
	}
}

func TestOneToManyMapping(t *testing.T) {
	// "ß" -> "ss" is a one-to-many mapping; "Straße" and "Strasse" collide.
	groups := FindConflicts([]Entry{
		{Path: "Straße"},
		{Path: "Strasse"},
	}, testTable(t))
	if len(groups) != 1 || groups[0].Norm != "strasse" || groups[0].Reason != "name" {
		t.Fatalf("unexpected groups: %+v", groups)
	}
	if !hasOrigin(&groups[0], "Straße", false) || !hasOrigin(&groups[0], "Strasse", false) {
		t.Fatalf("origins missing: %+v", groups[0].Origins)
	}
}

func TestParentPrefixTypeConflict(t *testing.T) {
	// "docs" is a file, but "Docs/readme.md" needs "docs" as a directory.
	// Full-path string comparison alone would never see this.
	groups := FindConflicts([]Entry{
		{Path: "docs"},
		{Path: "Docs/readme.md"},
	}, testTable(t))
	if len(groups) != 1 {
		t.Fatalf("expected exactly 1 group, got %d: %+v", len(groups), groups)
	}
	g := groups[0]
	if g.Reason != "type" || g.Norm != "docs" {
		t.Fatalf("unexpected group: %+v", g)
	}
	if !hasOrigin(&g, "docs", false) || !hasOrigin(&g, "Docs/readme.md", false) {
		t.Fatalf("origins missing: %+v", g.Origins)
	}
}

func TestTypeConflictShallowestDescendants(t *testing.T) {
	// Only the shallowest descendants belong in the group.
	groups := FindConflicts([]Entry{
		{Path: "a"},
		{Path: "A/b/c.txt"},
		{Path: "A/d.txt"},
	}, testTable(t))
	if len(groups) != 1 || groups[0].Reason != "type" {
		t.Fatalf("unexpected groups: %+v", groups)
	}
	g := groups[0]
	if !hasOrigin(&g, "A/d.txt", false) || !hasOrigin(&g, "A/b/c.txt", false) {
		// both are at depth 2 vs depth 3: only "A/d.txt" is shallowest
	}
	if !hasOrigin(&g, "A/d.txt", false) {
		t.Fatalf("shallowest descendant missing: %+v", g.Origins)
	}
	if hasOrigin(&g, "A/b/c.txt", false) {
		t.Fatalf("deeper descendant should be excluded: %+v", g.Origins)
	}
}

func TestTrailingSpaceAndDot(t *testing.T) {
	groups := FindConflicts([]Entry{
		{Path: "note.txt"},
		{Path: "note.txt "},
		{Path: "note.txt."},
	}, testTable(t))
	if len(groups) != 1 || groups[0].Reason != "name" || groups[0].Norm != "note.txt" {
		t.Fatalf("unexpected groups: %+v", groups)
	}
	if len(groups[0].Origins) != 3 {
		t.Fatalf("expected 3 origins, got %+v", groups[0].Origins)
	}
}

func TestTrailingSpaceInParentSegment(t *testing.T) {
	// Trailing-space rule must apply to parent segments too.
	groups := FindConflicts([]Entry{
		{Path: "dir /a.txt"},
		{Path: "dir/a.txt"},
	}, testTable(t))
	if len(groups) != 1 || groups[0].Norm != "dir/a.txt" {
		t.Fatalf("unexpected groups: %+v", groups)
	}
}

func TestReservedNames(t *testing.T) {
	groups := FindConflicts([]Entry{
		{Path: "build/aux.txt"},
		{Path: "build/ok.txt"},
	}, testTable(t))
	if len(groups) != 1 {
		t.Fatalf("expected exactly 1 group, got %+v", groups)
	}
	g := groups[0]
	if g.Reason != "reserved" || g.Norm != "build/aux.txt" {
		t.Fatalf("unexpected group: %+v", g)
	}
	if !hasOrigin(&g, "build/aux.txt", false) {
		t.Fatalf("origin missing: %+v", g.Origins)
	}
}

func TestReservedAndNameConflictBothReported(t *testing.T) {
	// A reserved-name problem is an independent root cause: it must not be
	// hidden by a name collision at the same path.
	groups := FindConflicts([]Entry{
		{Path: "CON"},
		{Path: "con"},
	}, testTable(t))
	if findGroup(groups, "name", "con") == nil {
		t.Fatalf("name group missing: %+v", groups)
	}
	if findGroup(groups, "reserved", "con") == nil {
		t.Fatalf("reserved group missing: %+v", groups)
	}
}

func TestMinimalGroups(t *testing.T) {
	// The child collision "A/x.txt" vs "a/x.txt" is fully implied by the
	// parent collision "A" vs "a" and must not be reported again.
	groups := FindConflicts([]Entry{
		{Path: "A", Dir: true},
		{Path: "a", Dir: true},
		{Path: "A/x.txt"},
		{Path: "a/x.txt"},
	}, testTable(t))
	if len(groups) != 1 {
		t.Fatalf("expected exactly 1 minimal group, got %d: %+v", len(groups), groups)
	}
	if groups[0].Norm != "a" || groups[0].Reason != "name" {
		t.Fatalf("unexpected group: %+v", groups[0])
	}
}

func TestInvalidEmptySegment(t *testing.T) {
	groups := FindConflicts([]Entry{
		{Path: "ok/ /x.txt"},
	}, testTable(t))
	if len(groups) != 1 || groups[0].Reason != "invalid" {
		t.Fatalf("unexpected groups: %+v", groups)
	}
}

func TestNoConflicts(t *testing.T) {
	groups := FindConflicts([]Entry{
		{Path: "src", Dir: true},
		{Path: "src/main.go"},
		{Path: "README.md"},
	}, testTable(t))
	if len(groups) != 0 {
		t.Fatalf("expected no conflicts, got %+v", groups)
	}
}

// TestWalkSyntheticTree builds a real directory tree and checks the
// end-to-end path: walk -> normalize -> minimal groups, with original
// names preserved.
func TestWalkSyntheticTree(t *testing.T) {
	root := t.TempDir()
	mk := func(rel string, dir bool) {
		p := filepath.Join(root, filepath.FromSlash(rel))
		var err error
		if dir {
			err = os.MkdirAll(p, 0o755)
		} else {
			err = os.WriteFile(p, []byte("x"), 0o644)
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	mk("note.txt", false)
	mk("note.txt ", false) // trailing-space twin
	mk("CON", true)        // reserved after folding
	mk("CON/inner.txt", false)
	mk("Ä.txt", false) // one-to-many fold twin of "ae.txt"
	mk("ae.txt", false)

	entries, err := walkRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	groups := FindConflicts(entries, testTable(t))

	g := findGroup(groups, "name", "note.txt")
	if g == nil || !hasOrigin(g, "note.txt ", false) || !hasOrigin(g, "note.txt", false) {
		t.Fatalf("trailing-space group wrong: %+v (all: %+v)", g, groups)
	}
	if findGroup(groups, "reserved", "con") == nil {
		t.Fatalf("reserved group missing: %+v", groups)
	}
	g = findGroup(groups, "name", "ae.txt")
	if g == nil || !hasOrigin(g, "Ä.txt", false) || !hasOrigin(g, "ae.txt", false) {
		t.Fatalf("one-to-many group wrong: %+v (all: %+v)", g, groups)
	}
	// "CON/inner.txt" sits under the already-reported "con" collision and
	// must not produce its own group.
	for _, gr := range groups {
		if gr.Norm == "con/inner.txt" {
			t.Fatalf("non-minimal descendant group reported: %+v", gr)
		}
	}
}
