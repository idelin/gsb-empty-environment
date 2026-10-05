package packer

import (
	"fmt"
	"math/rand"
	"reflect"
	"strings"
	"testing"
)

// MemNode is an in-memory file tree used by the tests.
type MemNode struct {
	name   string
	dir    bool
	link   bool
	target string
	kids   []*MemNode
}

func md(name string, kids ...*MemNode) *MemNode { return &MemNode{name: name, dir: true, kids: kids} }
func mf(name string) *MemNode                   { return &MemNode{name: name} }
func ml(name, target string) *MemNode           { return &MemNode{name: name, link: true, target: target} }

func (n *MemNode) Name() string       { return n.name }
func (n *MemNode) IsDir() bool        { return n.dir && !n.link }
func (n *MemNode) IsSymlink() bool    { return n.link }
func (n *MemNode) LinkTarget() string { return n.target }
func (n *MemNode) Children() ([]Node, error) {
	out := make([]Node, len(n.kids))
	for i, k := range n.kids {
		out[i] = k
	}
	return out, nil
}

func dumpTree(n *MemNode, indent string) string {
	s := indent + n.name
	switch {
	case n.link:
		s += " -> " + n.target
	case n.dir:
		s += "/"
	}
	s += "\n"
	for _, k := range n.kids {
		s += dumpTree(k, indent+"  ")
	}
	return s
}

func pathsOf(entries []Entry) []string {
	var out []string
	for _, e := range entries {
		out = append(out, e.Path)
	}
	return out
}

func collectPaths(t *testing.T, root Node, rs *RuleSet) []string {
	t.Helper()
	entries, _, err := Collect(root, rs, true)
	if err != nil {
		t.Fatal(err)
	}
	return pathsOf(entries)
}

func TestReincludeUnderExcludedDir(t *testing.T) {
	tree := md("",
		md("a", md("b", mf("c.txt"), mf("other.log"))),
		mf("top.txt"),
	)
	rs := mustRuleSet(t, true, "- /a", "+ /a/b/c.txt")
	want := []string{"a/b/c.txt", "top.txt"}
	if got := collectPaths(t, tree, rs); !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestDoubleStar(t *testing.T) {
	tree := md("",
		md("x", md("tmp", mf("one.txt")), md("y", md("tmp", mf("two.txt")))),
		md("tmp", mf("three.txt")),
		md("keep", mf("x.log")),
	)
	rs := mustRuleSet(t, true, "- **/tmp/**")
	want := []string{"x", "x/y", "keep", "keep/x.log"}
	if got := collectPaths(t, tree, rs); !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestEmptyDirIncluded(t *testing.T) {
	tree := md("",
		md("empty"),
		md("nested", md("also-empty")),
		mf("f.txt"),
	)
	rs := mustRuleSet(t, true)
	want := []string{"empty", "nested", "nested/also-empty", "f.txt"}
	got := collectPaths(t, tree, rs)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
	entries, _, _ := Collect(tree, rs, true)
	types := map[string]EntryType{}
	for _, e := range entries {
		types[e.Path] = e.Type
	}
	if types["empty"] != EntryDir || types["f.txt"] != EntryFile {
		t.Errorf("wrong entry types: %v", types)
	}
}

func TestSymlinkStoredNotFollowed(t *testing.T) {
	tree := md("",
		md("real", mf("secret.txt")),
		ml("link", "real"),
		ml("broken", "nonexistent"),
	)
	rs := mustRuleSet(t, true)
	want := []string{"real", "real/secret.txt", "link", "broken"}
	if got := collectPaths(t, tree, rs); !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
	entries, _, _ := Collect(tree, rs, true)
	for _, e := range entries {
		if e.Path == "link" {
			if e.Type != EntrySymlink || e.LinkTarget != "real" {
				t.Errorf("link entry = %+v, want symlink to real", e)
			}
		}
		if strings.HasPrefix(e.Path, "link/") {
			t.Errorf("descended into symlink: %v", e.Path)
		}
	}
}

func TestNormalizedRuleForms(t *testing.T) {
	tree := md("",
		md("sub", mf("keep.txt"), mf("other.log")),
	)
	rs := mustRuleSet(t, false, "+ ./sub//keep.txt")
	want := []string{"sub/keep.txt"}
	if got := collectPaths(t, tree, rs); !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestPruningSkipsSubtree(t *testing.T) {
	big := md("big")
	for i := 0; i < 10; i++ {
		big.kids = append(big.kids, md(fmt.Sprintf("d%d", i), mf("f.txt")))
	}
	tree := md("", big, md("small", mf("f.txt")))
	rs := mustRuleSet(t, true, "- /big")

	entries, stats, err := Collect(tree, rs, true)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"small", "small/f.txt"}
	if got := pathsOf(entries); !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
	if stats.Pruned != 1 {
		t.Errorf("Pruned = %d, want 1", stats.Pruned)
	}
	if stats.Visited != 4 { // root, big, small, small/f.txt
		t.Errorf("Visited = %d, want 4", stats.Visited)
	}

	ref, err := ReferenceCollect(tree, rs)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(entries, ref) {
		t.Errorf("pruned walk disagrees with reference:\n got %v\nwant %v", entries, ref)
	}
}

func TestNoPruneWhenReincludePossible(t *testing.T) {
	tree := md("", md("a", md("b", mf("c.txt"))))
	rs := mustRuleSet(t, true, "- /a", "+ /a/b/c.txt")
	_, stats, err := Collect(tree, rs, true)
	if err != nil {
		t.Fatal(err)
	}
	if stats.Pruned != 0 {
		t.Errorf("Pruned = %d, want 0 (a later rule re-includes a/b/c.txt)", stats.Pruned)
	}
	if stats.Visited != 4 {
		t.Errorf("Visited = %d, want 4", stats.Visited)
	}
}

func TestWinningRuleReported(t *testing.T) {
	tree := md("", mf("a.txt"), mf("b.log"))
	rs := mustRuleSet(t, true, "- *.txt", "+ a.txt")
	entries, _, err := Collect(tree, rs, true)
	if err != nil {
		t.Fatal(err)
	}
	rules := map[string]int{}
	for _, e := range entries {
		rules[e.Path] = e.Rule
	}
	if rules["a.txt"] != 1 {
		t.Errorf("a.txt winning rule = %d, want 1", rules["a.txt"])
	}
	if rules["b.log"] != -1 {
		t.Errorf("b.log winning rule = %d, want -1 (default)", rules["b.log"])
	}
}

func TestDefaultExcludeWalk(t *testing.T) {
	tree := md("",
		md("d1", mf("a.txt")),
		md("d2", mf("b.log")),
	)
	rs := mustRuleSet(t, false, "+ **/*.txt", "+ /d2")
	want := []string{"d1/a.txt", "d2", "d2/b.log"}
	if got := collectPaths(t, tree, rs); !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

var randNames = []string{"a", "b", "c", "tmp", "x.txt", "y.log", "sub", "keep.txt"}

func randTree(r *rand.Rand) *MemNode {
	root := &MemNode{name: "", dir: true}
	var build func(n *MemNode, depth int)
	build = func(n *MemNode, depth int) {
		used := map[string]bool{}
		count := r.Intn(5)
		for i := 0; i < count; i++ {
			name := randNames[r.Intn(len(randNames))]
			if used[name] {
				continue
			}
			used[name] = true
			switch roll := r.Intn(100); {
			case roll < 45:
				n.kids = append(n.kids, mf(name))
			case roll < 80 && depth < 4:
				d := md(name)
				n.kids = append(n.kids, d)
				build(d, depth+1)
			case roll < 90:
				n.kids = append(n.kids, ml(name, "a"))
			default:
				n.kids = append(n.kids, md(name)) // empty dir
			}
		}
	}
	build(root, 0)
	return root
}

func randRuleSet(r *rand.Rand, root *MemNode) *RuleSet {
	var paths []string
	var collect func(n *MemNode, prefix string)
	collect = func(n *MemNode, prefix string) {
		for _, k := range n.kids {
			p := prefix + "/" + k.name
			paths = append(paths, p)
			if k.IsDir() {
				collect(k, p)
			}
		}
	}
	collect(root, "")

	fragments := []string{"*.txt", "tmp", "**/tmp/**", "**", "?.log", "a", "b", "*/x.txt", "**/b", "keep.txt"}
	var lines []string
	for i, n := 0, r.Intn(7); i < n; i++ {
		action := "+ "
		if r.Intn(2) == 0 {
			action = "- "
		}
		var pat string
		switch r.Intn(3) {
		case 0:
			pat = fragments[r.Intn(len(fragments))]
		case 1:
			if len(paths) > 0 {
				pat = paths[r.Intn(len(paths))]
			} else {
				pat = "*"
			}
		case 2:
			if len(paths) > 0 {
				p := paths[r.Intn(len(paths))]
				segs := strings.Split(strings.TrimPrefix(p, "/"), "/")
				segs[r.Intn(len(segs))] = "*"
				pat = "/" + strings.Join(segs, "/")
			} else {
				pat = "*"
			}
		}
		if r.Intn(6) == 0 {
			pat += "/"
		}
		lines = append(lines, action+pat)
	}
	rules, err := ParseRules(strings.Join(lines, "\n"))
	if err != nil {
		panic(err)
	}
	rs := &RuleSet{Rules: rules, DefaultInclude: r.Intn(4) != 0}
	rs.Build()
	return rs
}

// TestDifferentialRandom cross-checks the pruning walk against the
// full-traversal reference on random finite trees and random rule sets,
// exercising '**', empty directories, symlinks and re-inclusion.
func TestDifferentialRandom(t *testing.T) {
	r := rand.New(rand.NewSource(1))
	for i := 0; i < 1000; i++ {
		tree := randTree(r)
		rs := randRuleSet(r, tree)
		got, _, err := Collect(tree, rs, true)
		if err != nil {
			t.Fatal(err)
		}
		want, err := ReferenceCollect(tree, rs)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got, want) {
			var ruleLines []string
			for j, rule := range rs.Rules {
				ruleLines = append(ruleLines, fmt.Sprintf("  %d: %s", j, rule.String()))
			}
			t.Fatalf("iteration %d mismatch (defaultInclude=%v)\nrules:\n%s\ntree:\n%s\ngot:  %v\nwant: %v",
				i, rs.DefaultInclude, strings.Join(ruleLines, "\n"), dumpTree(tree, ""), got, want)
		}
	}
}
