package pkgincr

import (
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// makeTree builds a file tree from a map. Keys are slash-separated paths;
// a trailing "/" creates an (empty) directory, a value starting with "@"
// creates a symlink to the remainder, anything else creates a regular
// file with the value as content.
func makeTree(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for rel, content := range files {
		p := filepath.Join(root, filepath.FromSlash(rel))
		switch {
		case strings.HasSuffix(rel, "/"):
			if err := os.MkdirAll(p, 0o755); err != nil {
				t.Fatal(err)
			}
		case strings.HasPrefix(content, "@"):
			if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(content[1:], p); err != nil {
				t.Fatal(err)
			}
		default:
			if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
}

func mustParse(t *testing.T, text string) []Rule {
	t.Helper()
	rules, err := ParseRules(text)
	if err != nil {
		t.Fatal(err)
	}
	return rules
}

// includedPaths returns the sorted set of included paths.
func includedPaths(res *Result) []string {
	var out []string
	for _, e := range res.Included() {
		out = append(out, e.Path)
	}
	sort.Strings(out)
	return out
}

func TestWalkReInclude(t *testing.T) {
	root := t.TempDir()
	makeTree(t, root, map[string]string{
		"build/keep.txt": "k",
		"build/junk.txt": "j",
		"build/sub/x.o":  "x",
		"src/main.go":    "package main",
	})
	rules := mustParse(t, `
- /build/
+ /build/keep.txt
`)
	res, err := Walk(root, rules)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"build/keep.txt", "src", "src/main.go"}
	if got := includedPaths(res); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("included = %v, want %v", got, want)
	}
	// "build" itself must be descended into (the re-include rule can match
	// below it), while "build/sub" is pruned: /build/keep.txt can never
	// match anything inside it.
	if len(res.Pruned) != 1 || res.Pruned[0] != "build/sub" {
		t.Errorf("pruned = %v, want [build/sub]", res.Pruned)
	}
	// Decisive rule for the re-included file is the include rule (#2).
	for _, e := range res.Entries {
		if e.Path == "build/keep.txt" {
			if e.Rule != 1 || e.ViaAncestor {
				t.Errorf("keep.txt: rule=%d viaAncestor=%v, want rule=1 direct", e.Rule, e.ViaAncestor)
			}
		}
		if e.Path == "build/junk.txt" {
			if e.Included || e.Rule != 0 || !e.ViaAncestor {
				t.Errorf("junk.txt: included=%v rule=%d viaAncestor=%v, want excluded via ancestor rule 0",
					e.Included, e.Rule, e.ViaAncestor)
			}
		}
	}
}

func TestWalkNestedReInclude(t *testing.T) {
	root := t.TempDir()
	makeTree(t, root, map[string]string{
		"a/b/c/d/yes.txt": "y",
		"a/b/c/no.txt":    "n",
		"a/b/other.txt":   "o",
	})
	rules := mustParse(t, `
- /a/
+ /a/b/
- /a/b/c/
+ /a/b/c/d/yes.txt
`)
	res, err := Walk(root, rules)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"a/b", "a/b/c/d/yes.txt", "a/b/other.txt"}
	if got := includedPaths(res); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("included = %v, want %v", got, want)
	}
}

func TestWalkPruning(t *testing.T) {
	root := t.TempDir()
	makeTree(t, root, map[string]string{
		"build/a.txt":     "a",
		"build/sub/b.txt": "b",
		"src/c.txt":       "c",
	})
	rules := mustParse(t, "- /build/\n")
	res, err := Walk(root, rules)
	if err != nil {
		t.Fatal(err)
	}
	if got := includedPaths(res); strings.Join(got, ",") != "src,src/c.txt" {
		t.Errorf("included = %v, want [src src/c.txt]", got)
	}
	if len(res.Pruned) != 1 || res.Pruned[0] != "build" {
		t.Errorf("pruned = %v, want [build]", res.Pruned)
	}
	// Children of the pruned directory must not have been visited.
	for _, e := range res.Entries {
		if strings.HasPrefix(e.Path, "build/") {
			t.Errorf("visited %q below pruned directory", e.Path)
		}
	}
}

func TestWalkEmptyDirs(t *testing.T) {
	root := t.TempDir()
	makeTree(t, root, map[string]string{
		"empty/":        "",
		"empty/nested/": "",
		"full/f.txt":    "f",
	})
	res, err := Walk(root, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"empty", "empty/nested", "full", "full/f.txt"}
	if got := includedPaths(res); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("included = %v, want %v (empty directories must be kept)", got, want)
	}

	// Excluded empty directory disappears; re-included one survives.
	rules := mustParse(t, "- /empty/\n+ /empty/nested/\n")
	res, err = Walk(root, rules)
	if err != nil {
		t.Fatal(err)
	}
	want = []string{"empty/nested", "full", "full/f.txt"}
	if got := includedPaths(res); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("included = %v, want %v", got, want)
	}
}

func TestWalkSymlinkNotFollowed(t *testing.T) {
	root := t.TempDir()
	makeTree(t, root, map[string]string{
		"real/inner.txt": "i",
		"link":           "@real",
		"filelink":       "@real/inner.txt",
	})
	res, err := Walk(root, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range res.Entries {
		if strings.HasPrefix(e.Path, "link/") {
			t.Errorf("descended into symlinked directory via %q", e.Path)
		}
		if e.Path == "link" {
			if !e.IsSymlink || e.IsDir || e.LinkTarget != "real" {
				t.Errorf("link: %+v, want symlink to real", e)
			}
		}
	}
	// A dir-only rule must not match a symlink that points at a directory.
	rules := mustParse(t, "- link/\n")
	res, err = Walk(root, rules)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range res.Entries {
		if e.Path == "link" && !e.Included {
			t.Error("dir-only rule matched symlink-to-directory")
		}
	}
}

// referenceIncluded is the independent reference: a full traversal with no
// pruning, deciding every path directly from the rule semantics — the
// decisive rule is the LAST rule matching the path itself or any ancestor
// directory; unmatched paths are included by default.
func referenceIncluded(t *testing.T, root string, rules []Rule) map[string]int {
	t.Helper()
	out := map[string]int{}
	var rec func(rel string)
	rec = func(rel string) {
		dir := filepath.Join(root, filepath.FromSlash(rel))
		f, err := os.Open(dir)
		if err != nil {
			t.Fatal(err)
		}
		infos, err := f.Readdir(-1)
		f.Close()
		if err != nil {
			t.Fatal(err)
		}
		for _, info := range infos {
			childRel := info.Name()
			if rel != "" {
				childRel = rel + "/" + info.Name()
			}
			isDir := info.IsDir() // false for symlinks: never followed
			segs := strings.Split(childRel, "/")
			decisive := -1
			for i := range rules {
				if rules[i].Matches(segs, isDir) {
					decisive = i
					continue
				}
				for k := 1; k < len(segs); k++ {
					if rules[i].Matches(segs[:k], true) {
						decisive = i
						break
					}
				}
			}
			if decisive == -1 || rules[decisive].Kind == Include {
				out[childRel] = decisive
			}
			if isDir {
				rec(childRel)
			}
		}
	}
	rec("")
	return out
}

// TestDifferential cross-checks the pruning walker against the full
// traversal reference on randomly generated finite trees and rule sets,
// exercising "**" patterns, empty directories, symlinks and normalized
// re-spellings of the same paths.
func TestDifferential(t *testing.T) {
	rng := rand.New(rand.NewSource(20261005))
	names := []string{"a", "b", "c", "build", "keep.txt", "x.o", "y.go", "d"}
	patternPool := []string{
		"*.o", "keep.txt", "build/", "build", "/a/b", "/build/", "a/**/b",
		"**/keep.txt", "/a/**", "**/b/**", "/a", "y.go", "/a/b/c",
		// Re-spellings of the same paths: normalization must make these
		// behave exactly like their canonical forms.
		"./a/b", "a//b", "/a/./b", "a/b/", "/build//", "./keep.txt",
	}
	for iter := 0; iter < 400; iter++ {
		root := t.TempDir()
		// Random finite tree, depth <= 3, including empty dirs and links.
		files := map[string]string{}
		var gen func(rel string, depth int)
		gen = func(rel string, depth int) {
			n := rng.Intn(4)
			used := map[string]bool{}
			for i := 0; i < n; i++ {
				name := names[rng.Intn(len(names))]
				if used[name] {
					continue
				}
				used[name] = true
				p := name
				if rel != "" {
					p = rel + "/" + name
				}
				switch r := rng.Intn(10); {
				case r < 4 && depth < 3:
					files[p+"/"] = "" // directory (possibly empty)
					gen(p, depth+1)
				case r == 4 && depth < 3:
					files[p+"/"] = "" // empty directory, no children
				case r == 5:
					files[p] = "@" + names[rng.Intn(len(names))] // symlink
				default:
					files[p] = fmt.Sprintf("content-%d", rng.Intn(100))
				}
			}
		}
		gen("", 0)
		makeTree(t, root, files)

		// Random ordered rule set.
		var lines []string
		for i := 0; i < rng.Intn(7); i++ {
			sigil := "+"
			if rng.Intn(2) == 0 {
				sigil = "-"
			}
			lines = append(lines, sigil+" "+patternPool[rng.Intn(len(patternPool))])
		}
		rules, err := ParseRules(strings.Join(lines, "\n"))
		if err != nil {
			t.Fatal(err)
		}

		res, err := Walk(root, rules)
		if err != nil {
			t.Fatal(err)
		}
		ref := referenceIncluded(t, root, rules)

		// Same included set, same decisive rule for every included path.
		got := map[string]int{}
		for _, e := range res.Included() {
			got[e.Path] = e.Rule
		}
		if len(got) != len(ref) {
			t.Fatalf("iter %d rules %q: included %d paths, reference %d\ngot: %v\nref: %v",
				iter, lines, len(got), len(ref), sortedKeys(got), sortedKeys(ref))
		}
		for p, dec := range ref {
			g, ok := got[p]
			if !ok {
				t.Fatalf("iter %d rules %q: %q included by reference but missing", iter, lines, p)
			}
			if g != dec {
				t.Fatalf("iter %d rules %q: %q decisive rule %d, reference %d", iter, lines, p, g, dec)
			}
		}
		// Nothing included may hide below a pruned directory.
		for _, pruned := range res.Pruned {
			prefix := pruned + "/"
			for p := range ref {
				if strings.HasPrefix(p, prefix) {
					t.Fatalf("iter %d rules %q: %q included below pruned %q", iter, lines, p, pruned)
				}
			}
		}
	}
}

func sortedKeys(m map[string]int) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
