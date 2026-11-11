package netsec

import (
	"errors"
	"fmt"
	"path"
	"sort"
	"strings"
)

var (
	ErrInvalidPath   = errors.New("netsec: invalid path")
	ErrMalformed     = errors.New("netsec: malformed manifest entry")
	ErrConflict      = errors.New("netsec: conflicting manifest entries")
	ErrCrossRoot     = errors.New("netsec: link target escapes the input root")
	ErrMissingTarget = errors.New("netsec: link target missing from manifest")
	ErrCycle         = errors.New("netsec: link resolution cycle")
)

type Kind int

const (
	KindFile Kind = iota
	KindDir
	KindLink
)

func (k Kind) String() string {
	switch k {
	case KindFile:
		return "file"
	case KindDir:
		return "dir"
	case KindLink:
		return "link"
	}
	return "unknown"
}

// Entry is one manifest object. Paths are slash-separated and relative to
// the input root. Link targets may be relative (resolved against the
// directory containing the link) or root-relative (leading "/"); they are
// never interpreted as host filesystem paths.
type Entry struct {
	Path   string
	Kind   Kind
	Target string
	Digest string
}

type Manifest struct {
	Entries []Entry
}

// PublishedFile is one file visible in the assembled input view.
type PublishedFile struct {
	Path   string
	Digest string
}

// View is the published input view: a flat set of files with digests.
type View struct {
	files map[string]string
}

func (v *View) Lookup(p string) (string, bool) {
	d, ok := v.files[p]
	return d, ok
}

func (v *View) Len() int { return len(v.files) }

func (v *View) List() []PublishedFile {
	out := make([]PublishedFile, 0, len(v.files))
	for p, d := range v.files {
		out = append(out, PublishedFile{Path: p, Digest: d})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

type graph struct {
	files    map[string]Entry
	dirs     map[string]bool
	links    map[string]Entry
	children map[string][]string
	memo     map[string]string
}

// Assemble validates the manifest and publishes the input view. It rejects
// links that escape the input root, form cycles, or point at objects not
// declared in the manifest.
func Assemble(m Manifest) (*View, error) {
	g := &graph{
		files:    map[string]Entry{},
		dirs:     map[string]bool{"": true},
		links:    map[string]Entry{},
		children: map[string][]string{},
		memo:     map[string]string{},
	}
	for _, e := range m.Entries {
		p, err := cleanPath(e.Path)
		if err != nil {
			return nil, fmt.Errorf("%w: %q", ErrInvalidPath, e.Path)
		}
		e.Path = p
		if err := checkEntry(e); err != nil {
			return nil, err
		}
		if err := g.add(e); err != nil {
			return nil, err
		}
	}
	if err := g.materializeDirs(m.Entries); err != nil {
		return nil, err
	}
	g.indexChildren()
	for p := range g.links {
		if _, err := g.resolve(p, map[string]bool{}); err != nil {
			return nil, err
		}
	}
	view := &View{files: map[string]string{}}
	if err := g.expandDir("", "", map[string]bool{}, view.files); err != nil {
		return nil, err
	}
	return view, nil
}

func cleanPath(p string) (string, error) {
	if p == "" || strings.HasPrefix(p, "/") {
		return "", ErrInvalidPath
	}
	c := path.Clean(p)
	if c == "." || c == ".." || strings.HasPrefix(c, "../") {
		return "", ErrInvalidPath
	}
	return c, nil
}

func checkEntry(e Entry) error {
	switch e.Kind {
	case KindFile:
		if e.Digest == "" {
			return fmt.Errorf("%w: file %q has no digest", ErrMalformed, e.Path)
		}
		if e.Target != "" {
			return fmt.Errorf("%w: file %q carries a link target", ErrMalformed, e.Path)
		}
	case KindDir:
		if e.Target != "" || e.Digest != "" {
			return fmt.Errorf("%w: dir %q carries a target or digest", ErrMalformed, e.Path)
		}
	case KindLink:
		if e.Target == "" {
			return fmt.Errorf("%w: link %q has no target", ErrMalformed, e.Path)
		}
		if e.Digest != "" {
			return fmt.Errorf("%w: link %q carries a digest", ErrMalformed, e.Path)
		}
	default:
		return fmt.Errorf("%w: unknown kind for %q", ErrMalformed, e.Path)
	}
	return nil
}

func (g *graph) add(e Entry) error {
	switch e.Kind {
	case KindFile:
		if _, ok := g.files[e.Path]; ok {
			return conflictErr(e.Path)
		}
		if _, ok := g.links[e.Path]; ok {
			return conflictErr(e.Path)
		}
		if g.dirs[e.Path] {
			return conflictErr(e.Path)
		}
		g.files[e.Path] = e
	case KindLink:
		if _, ok := g.links[e.Path]; ok {
			return conflictErr(e.Path)
		}
		if _, ok := g.files[e.Path]; ok {
			return conflictErr(e.Path)
		}
		// A link may share its path with a directory: the link replaces
		// the directory in the published view.
		g.links[e.Path] = e
	case KindDir:
		if _, ok := g.files[e.Path]; ok {
			return conflictErr(e.Path)
		}
		g.dirs[e.Path] = true
	}
	return nil
}

func (g *graph) materializeDirs(entries []Entry) error {
	for _, e := range entries {
		for d := parent(e.Path); ; d = parent(d) {
			if _, ok := g.files[d]; ok {
				return conflictErr(d)
			}
			g.dirs[d] = true
			if d == "" {
				break
			}
		}
	}
	return nil
}

func (g *graph) indexChildren() {
	add := func(p string) {
		par := parent(p)
		g.children[par] = append(g.children[par], p)
	}
	for p := range g.files {
		add(p)
	}
	for p := range g.links {
		add(p)
	}
	for p := range g.dirs {
		if p != "" {
			add(p)
		}
	}
	for _, list := range g.children {
		sort.Strings(list)
	}
}

// resolve follows a link chain to its final non-link object, memoizing
// results. Cycles and missing targets are errors.
func (g *graph) resolve(p string, visiting map[string]bool) (string, error) {
	if final, ok := g.memo[p]; ok {
		return final, nil
	}
	if visiting[p] {
		return "", fmt.Errorf("%w: link %q", ErrCycle, p)
	}
	visiting[p] = true
	target, err := resolveTarget(p, g.links[p].Target)
	if err != nil {
		return "", err
	}
	var final string
	if _, ok := g.links[target]; ok {
		final, err = g.resolve(target, visiting)
		if err != nil {
			return "", err
		}
	} else if g.dirs[target] {
		final = target
	} else if _, ok := g.files[target]; ok {
		final = target
	} else {
		return "", fmt.Errorf("%w: link %q -> %q", ErrMissingTarget, p, target)
	}
	g.memo[p] = final
	return final, nil
}

// resolveTarget normalizes a link target to a root-relative object path.
// Relative targets resolve against the directory containing the link; a
// leading "/" means the input root, never the host filesystem.
func resolveTarget(linkPath, target string) (string, error) {
	var c string
	if strings.HasPrefix(target, "/") {
		c = strings.TrimPrefix(path.Clean(target), "/")
	} else {
		c = path.Clean(path.Join(parent(linkPath), target))
	}
	if c == "." {
		c = ""
	}
	if c == ".." || strings.HasPrefix(c, "../") {
		return "", fmt.Errorf("%w: link %q -> %q", ErrCrossRoot, linkPath, target)
	}
	return c, nil
}

// expandDir publishes the subtree rooted at dir under prefix. Links are
// followed through their resolved targets; a link replaces any directory
// declared at the same path. The visiting set rejects directory cycles
// introduced by links pointing at ancestor directories.
func (g *graph) expandDir(dir, prefix string, visiting map[string]bool, out map[string]string) error {
	if visiting[dir] {
		return fmt.Errorf("%w: directory %q links back to itself", ErrCycle, displayDir(dir))
	}
	visiting[dir] = true
	defer delete(visiting, dir)
	for _, child := range g.children[dir] {
		dest := path.Base(child)
		if prefix != "" {
			dest = prefix + "/" + dest
		}
		if _, ok := g.links[child]; ok {
			final := g.memo[child]
			if g.dirs[final] {
				if err := g.expandDir(final, dest, visiting, out); err != nil {
					return err
				}
				continue
			}
			out[dest] = g.files[final].Digest
			continue
		}
		if f, ok := g.files[child]; ok {
			out[dest] = f.Digest
			continue
		}
		if err := g.expandDir(child, dest, visiting, out); err != nil {
			return err
		}
	}
	return nil
}

func parent(p string) string {
	d := path.Dir(p)
	if d == "." {
		return ""
	}
	return d
}

func displayDir(d string) string {
	if d == "" {
		return "<root>"
	}
	return d
}

func conflictErr(p string) error {
	return fmt.Errorf("%w: %q", ErrConflict, p)
}
