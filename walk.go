package packer

import "strings"

// EntryType classifies a packaged entry.
type EntryType int

const (
	EntryFile EntryType = iota
	EntryDir
	EntrySymlink
)

func (t EntryType) String() string {
	switch t {
	case EntryDir:
		return "dir"
	case EntrySymlink:
		return "symlink"
	default:
		return "file"
	}
}

// Entry is one included path together with its winning rule.
type Entry struct {
	Path       string // slash-separated, relative to the root
	Type       EntryType
	LinkTarget string // set for symlinks; the link itself, never followed
	Rule       int    // winning rule index, -1 when the default decided
}

// Node abstracts a file tree so the same engine walks real filesystems and
// in-memory test trees.
type Node interface {
	Name() string
	IsDir() bool
	IsSymlink() bool
	LinkTarget() string
	Children() ([]Node, error)
}

// Stats reports traversal counters.
type Stats struct {
	Visited int
	Pruned  int // directories whose subtree was skipped
}

// Collect walks root and returns every included entry with its winning
// rule. When prune is true, directories whose subtree provably cannot
// contain any included path are skipped entirely. Symlinks are stored as
// entries and never descended into.
func Collect(root Node, rs *RuleSet, prune bool) ([]Entry, Stats, error) {
	var entries []Entry
	var stats Stats
	var walk func(n Node, segs []string) error
	walk = func(n Node, segs []string) error {
		stats.Visited++
		if len(segs) > 0 {
			d := rs.Decide(segs, n.IsDir())
			if d.Included {
				entries = append(entries, entryFor(n, segs, d.Rule))
			} else if n.IsDir() && prune && !rs.canReinclude(d.Rule, segs) {
				stats.Pruned++
				return nil
			}
		}
		if !n.IsDir() {
			return nil
		}
		kids, err := n.Children()
		if err != nil {
			return err
		}
		for _, k := range kids {
			if err := walk(k, appendSeg(segs, k.Name())); err != nil {
				return err
			}
		}
		return nil
	}
	err := walk(root, nil)
	return entries, stats, err
}

// ReferenceCollect is the straightforward reference: a full traversal of
// the finite tree with no pruning, deciding every path independently. It
// exists to cross-check Collect; their results must be identical.
func ReferenceCollect(root Node, rs *RuleSet) ([]Entry, error) {
	var entries []Entry
	var walk func(n Node, segs []string) error
	walk = func(n Node, segs []string) error {
		if len(segs) > 0 {
			d := rs.Decide(segs, n.IsDir())
			if d.Included {
				entries = append(entries, entryFor(n, segs, d.Rule))
			}
		}
		if !n.IsDir() {
			return nil
		}
		kids, err := n.Children()
		if err != nil {
			return err
		}
		for _, k := range kids {
			if err := walk(k, appendSeg(segs, k.Name())); err != nil {
				return err
			}
		}
		return nil
	}
	err := walk(root, nil)
	return entries, err
}

func entryFor(n Node, segs []string, rule int) Entry {
	e := Entry{Path: strings.Join(segs, "/"), Rule: rule}
	switch {
	case n.IsSymlink():
		e.Type = EntrySymlink
		e.LinkTarget = n.LinkTarget()
	case n.IsDir():
		e.Type = EntryDir
	default:
		e.Type = EntryFile
	}
	return e
}

func appendSeg(segs []string, name string) []string {
	out := make([]string, len(segs)+1)
	copy(out, segs)
	out[len(segs)] = name
	return out
}
