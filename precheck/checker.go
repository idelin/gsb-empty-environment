package precheck

import (
	"fmt"
	"sort"
	"strings"
)

// EntryType distinguishes files from directories; the distinction matters
// because a file cannot also serve as a parent directory.
type EntryType string

const (
	File EntryType = "file"
	Dir  EntryType = "dir"
)

// Entry is one archive member. Path keeps its original spelling; only the
// folded form is used for comparison, so originals are preserved in output.
type Entry struct {
	Path string    `json:"path"`
	Type EntryType `json:"type"`
}

// ConflictGroup is a minimal conflict unit: exactly the entries that collide
// on one folded key (or violate a rule there), nothing more.
type ConflictGroup struct {
	Kind    string   `json:"kind"` // name-collision | type-conflict | violation
	Key     string   `json:"key"`  // folded path the group collides on
	Entries []Entry  `json:"entries"`
	Issues  []string `json:"issues,omitempty"`
}

// Report is the precheck result. An empty Groups slice means the archive is
// safe to import.
type Report struct {
	Groups []ConflictGroup `json:"groups"`
}

// node is one folded segment in the path trie. Tracking original prefix
// spellings per node is what lets the checker catch parent-prefix collisions
// that a flat full-path string comparison would miss.
type node struct {
	children map[string]*node
	entries  []Entry         // explicit entries whose path ends at this node
	prefixes map[string]bool // original prefix spellings that pass through this node
	examples []Entry         // sample descendant entries, for type-conflict context
}

func newNode() *node {
	return &node{prefixes: map[string]bool{}}
}

type violation struct {
	entry  Entry
	issues []SegmentIssue
}

// Checker accumulates entries and derives conflict groups.
type Checker struct {
	table      *FoldTable
	root       *node
	violations map[string][]violation // folded path -> violations found there
}

func NewChecker(t *FoldTable) *Checker {
	return &Checker{
		table:      t,
		root:       newNode(),
		violations: map[string][]violation{},
	}
}

// SplitPath normalizes separators and drops empty and "." segments.
func SplitPath(p string) []string {
	p = strings.ReplaceAll(p, "\\", "/")
	var out []string
	for _, s := range strings.Split(p, "/") {
		if s == "" || s == "." {
			continue
		}
		out = append(out, s)
	}
	return out
}

// Add inserts one entry into the trie.
func (c *Checker) Add(e Entry) {
	segs := SplitPath(e.Path)
	if len(segs) == 0 {
		return
	}
	ancestors := []*node{c.root}
	n := c.root
	var folded, orig []string
	for _, s := range segs {
		norm, issues := c.table.NormalizeSegment(s)
		folded = append(folded, norm)
		orig = append(orig, s)
		if len(issues) > 0 {
			key := strings.Join(folded, "/")
			c.violations[key] = append(c.violations[key], violation{entry: e, issues: issues})
		}
		if n.children == nil {
			n.children = map[string]*node{}
		}
		child, ok := n.children[norm]
		if !ok {
			child = newNode()
			n.children[norm] = child
		}
		n = child
		n.prefixes[strings.Join(orig, "/")] = true
		ancestors = append(ancestors, n)
	}
	n.entries = append(n.entries, e)
	// Record this entry as a subtree example on every ancestor so that
	// file-vs-directory type conflicts can show what lives underneath.
	for _, a := range ancestors[:len(ancestors)-1] {
		if len(a.examples) < 3 {
			a.examples = append(a.examples, e)
		}
	}
}

// Report walks the trie and emits the minimal conflict groups.
func (c *Checker) Report() *Report {
	var groups []ConflictGroup
	var walk func(n *node, key string)
	walk = func(n *node, key string) {
		// Members colliding at this folded node: explicit entries plus every
		// distinct original prefix spelling that passed through (implicit
		// parent directories).
		members := map[string]Entry{}
		for _, e := range n.entries {
			members[e.Path] = e
		}
		for p := range n.prefixes {
			if _, ok := members[p]; !ok {
				members[p] = Entry{Path: p, Type: Dir}
			}
		}
		types := map[EntryType]bool{}
		var fileEntries []Entry
		for _, e := range members {
			types[e.Type] = true
			if e.Type == File {
				fileEntries = append(fileEntries, e)
			}
		}
		switch {
		case len(members) > 1:
			kind := "name-collision"
			entries := sortedEntries(members)
			var issues []string
			if len(types) > 1 {
				kind = "type-conflict"
				entries = dedupEntries(append(entries, n.examples...))
				if len(n.children) > 0 && len(fileEntries) > 0 {
					issues = append(issues, "file entry is also used as a parent directory")
				}
			}
			groups = append(groups, ConflictGroup{
				Kind:    kind,
				Key:     key,
				Entries: entries,
				Issues:  issues,
			})
		case len(n.children) > 0 && len(fileEntries) > 0:
			// A file entry whose folded path is also used as a parent.
			entries := append([]Entry{}, fileEntries...)
			entries = append(entries, n.examples...)
			groups = append(groups, ConflictGroup{
				Kind:    "type-conflict",
				Key:     key,
				Entries: dedupEntries(entries),
				Issues:  []string{"file entry is also used as a parent directory"},
			})
		}
		if vs, ok := c.violations[key]; ok {
			var entries []Entry
			var msgs []string
			for _, v := range vs {
				entries = append(entries, v.entry)
				for _, is := range v.issues {
					msgs = append(msgs, is.String())
				}
			}
			sort.Strings(msgs)
			groups = append(groups, ConflictGroup{
				Kind:    "violation",
				Key:     key,
				Entries: dedupEntries(entries),
				Issues:  msgs,
			})
		}
		keys := make([]string, 0, len(n.children))
		for k := range n.children {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			childKey := k
			if key != "" {
				childKey = key + "/" + k
			}
			walk(n.children[k], childKey)
		}
	}
	walk(c.root, "")
	sort.Slice(groups, func(i, j int) bool {
		if groups[i].Key != groups[j].Key {
			return groups[i].Key < groups[j].Key
		}
		return groups[i].Kind < groups[j].Kind
	})
	return &Report{Groups: groups}
}

func sortedEntries(m map[string]Entry) []Entry {
	out := make([]Entry, 0, len(m))
	for _, e := range m {
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Path != out[j].Path {
			return out[i].Path < out[j].Path
		}
		return out[i].Type < out[j].Type
	})
	return out
}

func dedupEntries(in []Entry) []Entry {
	seen := map[string]bool{}
	var out []Entry
	for _, e := range in {
		k := fmt.Sprintf("%s\x00%s", e.Path, e.Type)
		if !seen[k] {
			seen[k] = true
			out = append(out, e)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Path != out[j].Path {
			return out[i].Path < out[j].Path
		}
		return out[i].Type < out[j].Type
	})
	return out
}
