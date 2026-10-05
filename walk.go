package pkgincr

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Entry describes one visited path inside the walked tree.
type Entry struct {
	Path        string // root-relative, '/'-separated, normalized
	IsDir       bool
	IsSymlink   bool
	LinkTarget  string // valid when IsSymlink
	Size        int64
	ModTime     time.Time
	Mode        os.FileMode
	Included    bool
	Rule        int  // index of the decisive rule, -1 for the default include
	ViaAncestor bool // decisive rule matched an ancestor directory, not the path itself
}

// Result is the outcome of a Walk.
type Result struct {
	// Entries lists every visited path in deterministic (sorted,
	// depth-first) order, both included and excluded ones. Children of
	// pruned directories do not appear.
	Entries []*Entry
	// Pruned lists excluded directories whose subtrees were skipped
	// because no later rule could possibly match anything inside them.
	Pruned []string
}

// Included returns the subset of entries selected for packaging.
func (r *Result) Included() []*Entry {
	var out []*Entry
	for _, e := range r.Entries {
		if e.Included {
			out = append(out, e)
		}
	}
	return out
}

// Walk traverses the tree rooted at root, deciding for every path whether
// it is included according to the ordered rules.
//
// The decisive rule for a path is the last rule that matches the path
// itself or any of its ancestor directories; if no rule matches, the path
// is included by default. Excluded directories whose subtree cannot
// contain any re-included path (no later rule can match a descendant) are
// pruned: their children are never visited. Symlinks are recorded as
// links and never followed.
func Walk(root string, rules []Rule) (*Result, error) {
	w := &walker{rules: rules, root: root}
	if err := w.walkDir("", -1); err != nil {
		return nil, err
	}
	return &w.res, nil
}

type walker struct {
	rules []Rule
	root  string
	res   Result
}

// walkDir visits the direct children of the directory at rel ("" for the
// root). inherited is the decisive rule index of the parent directory
// (-1 when no rule has matched so far).
func (w *walker) walkDir(rel string, inherited int) error {
	dir := w.root
	if rel != "" {
		dir = filepath.Join(w.root, filepath.FromSlash(rel))
	}
	f, err := os.Open(dir)
	if err != nil {
		return err
	}
	infos, err := f.Readdir(-1) // Lstat semantics: symlinks stay symlinks
	f.Close()
	if err != nil {
		return err
	}
	sort.Slice(infos, func(i, j int) bool { return infos[i].Name() < infos[j].Name() })
	for _, info := range infos {
		childRel := info.Name()
		if rel != "" {
			childRel = rel + "/" + info.Name()
		}
		entry, decisive, err := w.eval(childRel, info, inherited)
		if err != nil {
			return err
		}
		w.res.Entries = append(w.res.Entries, entry)
		if !entry.IsDir {
			continue
		}
		if entry.Included || w.canMatchBelow(decisive, childRel) {
			if err := w.walkDir(childRel, decisive); err != nil {
				return err
			}
		} else {
			w.res.Pruned = append(w.res.Pruned, childRel)
		}
	}
	return nil
}

// eval computes the decision for one path. The effective decisive rule is
// the later of the path's own last matching rule and the rule inherited
// from its ancestors.
func (w *walker) eval(rel string, info os.FileInfo, inherited int) (*Entry, int, error) {
	e := &Entry{
		Path:    rel,
		Size:    info.Size(),
		ModTime: info.ModTime(),
		Mode:    info.Mode(),
	}
	if info.Mode()&os.ModeSymlink != 0 {
		e.IsSymlink = true
		target, err := os.Readlink(filepath.Join(w.root, filepath.FromSlash(rel)))
		if err != nil {
			return nil, -1, err
		}
		e.LinkTarget = target
	} else if info.IsDir() {
		e.IsDir = true
	}
	segs := strings.Split(rel, "/")
	own := LastMatch(w.rules, segs, e.IsDir)
	decisive := own
	if inherited > decisive {
		decisive = inherited
		e.ViaAncestor = true
	}
	e.Rule = decisive
	e.Included = decisive == -1 || w.rules[decisive].Kind == Include
	return e, decisive, nil
}

// canMatchBelow reports whether any rule after the decisive one could
// match a path strictly below the excluded directory rel. If not, the
// whole subtree is guaranteed to be excluded and can be pruned.
func (w *walker) canMatchBelow(decisive int, rel string) bool {
	segs := strings.Split(rel, "/")
	for j := decisive + 1; j < len(w.rules); j++ {
		if w.rules[j].CanMatchDescendant(segs) {
			return true
		}
	}
	return false
}
