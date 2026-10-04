package precheck

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// asciiCaseMappings folds ASCII upper case to lower case.
func asciiCaseMappings() map[string]string {
	m := map[string]string{}
	for r := 'A'; r <= 'Z'; r++ {
		m[string(r)] = string(r + 32)
	}
	return m
}

// testTable folds ASCII case, maps ß -> ss and é -> e, trims trailing
// spaces/dots, and reserves classic DOS device names.
func testTable() *FoldTable {
	m := asciiCaseMappings()
	m["ß"] = "ss"
	m["é"] = "e"
	return NewFoldTable(m, []string{"CON", "PRN", "AUX", "NUL", "COM1", "LPT1"}, " .")
}

func check(t *testing.T, entries []Entry) *Report {
	t.Helper()
	c := NewChecker(testTable())
	for _, e := range entries {
		c.Add(e)
	}
	return c.Report()
}

func findGroup(r *Report, key string) *ConflictGroup {
	for i := range r.Groups {
		if r.Groups[i].Key == key && r.Groups[i].Kind != "violation" {
			return &r.Groups[i]
		}
	}
	return nil
}

func findViolations(r *Report, key string) *ConflictGroup {
	for i := range r.Groups {
		if r.Groups[i].Key == key && r.Groups[i].Kind == "violation" {
			return &r.Groups[i]
		}
	}
	return nil
}

func groupPaths(g *ConflictGroup) []string {
	var out []string
	for _, e := range g.Entries {
		out = append(out, e.Path)
	}
	sort.Strings(out)
	return out
}

func assertPaths(t *testing.T, g *ConflictGroup, want ...string) {
	t.Helper()
	got := groupPaths(g)
	want = append([]string{}, want...)
	sort.Strings(want)
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("group %q (%s) paths = %v, want %v", g.Key, g.Kind, got, want)
	}
}

// Multi-segment folding: collisions must be detected at every depth of the
// tree, not just for full path strings.
func TestMultiSegmentFoldCollision(t *testing.T) {
	r := check(t, []Entry{
		{Path: "Docs", Type: Dir},
		{Path: "DOCS", Type: Dir},
		{Path: "Docs/Alpha/readme.txt", Type: File},
		{Path: "docs/ALPHA/README.TXT", Type: File},
	})

	if g := findGroup(r, "docs"); g == nil {
		t.Error("missing collision group for folded dir 'docs'")
	} else {
		// The implicit parent of docs/ALPHA/README.TXT is a third spelling.
		assertPaths(t, g, "DOCS", "Docs", "docs")
		if g.Kind != "name-collision" {
			t.Errorf("kind = %q, want name-collision", g.Kind)
		}
	}
	// Alpha/ALPHA never appear as explicit entries; the collision is only
	// visible via the parent prefix of the files below them.
	if g := findGroup(r, "docs/alpha"); g == nil {
		t.Error("missing implicit parent collision group for 'docs/alpha'")
	} else {
		assertPaths(t, g, "Docs/Alpha", "docs/ALPHA")
	}
	if g := findGroup(r, "docs/alpha/readme.txt"); g == nil {
		t.Error("missing file collision group")
	} else {
		assertPaths(t, g, "Docs/Alpha/readme.txt", "docs/ALPHA/README.TXT")
	}
}

// One-to-many mapping: several originals fold to one key; the group must
// preserve every original name.
func TestOneToManyMappingAndOriginalPreservation(t *testing.T) {
	r := check(t, []Entry{
		{Path: "Résumé.md", Type: File},
		{Path: "RESUME.md", Type: File},
		{Path: "resume.md", Type: File},
	})
	if len(r.Groups) != 1 {
		t.Fatalf("expected exactly 1 group, got %d: %+v", len(r.Groups), r.Groups)
	}
	g := r.Groups[0]
	if g.Key != "resume.md" {
		t.Errorf("key = %q, want resume.md", g.Key)
	}
	assertPaths(t, &g, "RESUME.md", "Résumé.md", "resume.md")
}

// A flat full-path comparison would find no collision here; the conflict
// only exists at the parent prefix.
func TestParentPrefixNotMissed(t *testing.T) {
	r := check(t, []Entry{
		{Path: "A/b.txt", Type: File},
		{Path: "a/c.txt", Type: File},
	})
	g := findGroup(r, "a")
	if g == nil {
		t.Fatal("parent-prefix collision between 'A' and 'a' was missed")
	}
	assertPaths(t, g, "A", "a")
	for _, e := range g.Entries {
		if e.Type != Dir {
			t.Errorf("entry %q type = %q, want dir", e.Path, e.Type)
		}
	}
	if findGroup(r, "a/b.txt") != nil || findGroup(r, "a/c.txt") != nil {
		t.Error("files themselves do not collide; no groups expected for them")
	}
}

// Parent-directory types: file-vs-dir at the same folded node, and a file
// whose folded path is used as a parent directory.
func TestParentDirectoryTypeConflicts(t *testing.T) {
	r := check(t, []Entry{
		{Path: "data", Type: Dir},
		{Path: "DATA", Type: File},
		{Path: "Report", Type: File},
		{Path: "report/summary.txt", Type: File},
	})

	g := findGroup(r, "data")
	if g == nil || g.Kind != "type-conflict" {
		t.Fatalf("expected type-conflict at 'data', got %+v", g)
	}
	assertPaths(t, g, "DATA", "data")

	g = findGroup(r, "report")
	if g == nil || g.Kind != "type-conflict" {
		t.Fatalf("expected type-conflict at 'report', got %+v", g)
	}
	// The file, the implicit directory spelling, and a descendant example.
	assertPaths(t, g, "Report", "report", "report/summary.txt")
	if len(g.Issues) == 0 {
		t.Error("expected an explanatory issue for the file-as-parent conflict")
	}
}

// Trailing-space rule: "notes " collides with "notes" after trimming, and
// the trim itself is reported as a violation.
func TestTrailingSpaceRule(t *testing.T) {
	r := check(t, []Entry{
		{Path: "notes ", Type: File},
		{Path: "notes", Type: File},
	})
	g := findGroup(r, "notes")
	if g == nil {
		t.Fatal("collision after trailing-space trim was missed")
	}
	assertPaths(t, g, "notes", "notes ")
	v := findViolations(r, "notes")
	if v == nil {
		t.Fatal("missing trailing-trim violation")
	}
	if !strings.Contains(v.Issues[0], "trailing") {
		t.Errorf("unexpected violation message: %v", v.Issues)
	}
	assertPaths(t, v, "notes ")
}

// Reserved names apply to folded segments and to stems before an extension.
func TestReservedNames(t *testing.T) {
	r := check(t, []Entry{
		{Path: "CON.txt", Type: File},
		{Path: "dir/aux", Type: File},
		{Path: "ok.txt", Type: File},
	})
	if v := findViolations(r, "con.txt"); v == nil {
		t.Error("missing reserved-name violation for CON.txt")
	}
	if v := findViolations(r, "dir/aux"); v == nil {
		t.Error("missing reserved-name violation for dir/aux")
	}
	if findViolations(r, "ok.txt") != nil {
		t.Error("ok.txt should not be flagged")
	}
}

// Multi-character mapping: ß -> ss participates in folding like any other
// table entry.
func TestMultiCharFoldMapping(t *testing.T) {
	r := check(t, []Entry{
		{Path: "STRASSE.md", Type: File},
		{Path: "straße.md", Type: File},
	})
	if len(r.Groups) != 1 {
		t.Fatalf("expected 1 group, got %+v", r.Groups)
	}
	if r.Groups[0].Key != "strasse.md" {
		t.Errorf("key = %q, want strasse.md", r.Groups[0].Key)
	}
	assertPaths(t, &r.Groups[0], "STRASSE.md", "straße.md")
}

// A segment that normalizes to nothing is a violation.
func TestEmptyNameAfterTrim(t *testing.T) {
	r := check(t, []Entry{{Path: "dir/...", Type: File}})
	v := findViolations(r, "dir/")
	if v == nil {
		// key is "dir/" + "" joined as "dir/"
		v = findViolations(r, "dir/")
	}
	if v == nil {
		t.Fatalf("missing empty-name violation, groups: %+v", r.Groups)
	}
	assertPaths(t, v, "dir/...")
}

// Clean trees produce an empty report.
func TestCleanTree(t *testing.T) {
	r := check(t, []Entry{
		{Path: "src", Type: Dir},
		{Path: "src/main.go", Type: File},
		{Path: "README.md", Type: File},
	})
	if len(r.Groups) != 0 {
		t.Errorf("expected no groups, got %+v", r.Groups)
	}
}

// End-to-end over a real synthetic directory tree on disk. Names use the
// custom ① -> 1 mapping so they stay distinct on a case-insensitive
// filesystem (which would itself fold ß -> ss) while still exercising
// multi-segment folding through the table.
func TestScanDirSyntheticTree(t *testing.T) {
	root := t.TempDir()
	mk := func(rel string, isDir bool) {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if isDir {
			if err := os.MkdirAll(p, 0o755); err != nil {
				t.Fatal(err)
			}
			return
		}
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	mk("file①/a.txt", false)
	mk("file1/b.txt", false)
	mk("①notes.md", false)
	mk("1notes.md", false)

	table := testTable()
	table.Mappings["①"] = "1"
	table.compile()

	entries, err := ScanDir(root)
	if err != nil {
		t.Fatal(err)
	}
	c := NewChecker(table)
	for _, e := range entries {
		c.Add(e)
	}
	r := c.Report()

	if g := findGroup(r, "file1"); g == nil {
		t.Error("missed parent collision between 'file①' and 'file1'")
	} else {
		assertPaths(t, g, "file1", "file①")
	}
	if g := findGroup(r, "1notes.md"); g == nil {
		t.Error("missed collision between '①notes.md' and '1notes.md'")
	} else {
		assertPaths(t, g, "1notes.md", "①notes.md")
	}
}
