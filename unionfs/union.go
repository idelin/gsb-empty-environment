package unionfs

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"sort"
	"strings"
	"sync"
)

const maxSymlinkHops = 255

// Union is a read-only overlay of ordered tar layers. Index 0 is the lowest
// layer; the last layer is the topmost one.
type Union struct {
	layers []*Layer

	once sync.Once
	err  error

	nodes    map[string]*node
	visible  map[string]bool
	children map[string]map[string]struct{}
}

// New builds a union from the given stack of layers. No archives are read
// until the union is first queried.
func New(layers ...*Layer) *Union {
	for i, l := range layers {
		l.idx = i
	}
	return &Union{layers: layers}
}

func (u *Union) build() error {
	u.once.Do(func() {
		for _, l := range u.layers {
			if err := l.ensureIndexed(); err != nil {
				u.err = err
				return
			}
		}
		if err := u.fold(); err != nil {
			u.err = err
		}
	})
	return u.err
}

func (u *Union) fold() error {
	u.nodes = map[string]*node{}
	u.visible = map[string]bool{}
	u.children = map[string]map[string]struct{}{}

	// 1. Highest-layer node wins for every path; implicit directories are
	// synthesized for any missing ancestor.
	for _, l := range u.layers {
		for name, n := range l.nodes {
			if existing, ok := u.nodes[name]; !ok || n.layer > existing.layer {
				u.nodes[name] = n
			}
		}
	}
	if _, ok := u.nodes["."]; !ok {
		u.nodes["."] = &node{path: ".", typ: typeDir, mode: os.ModeDir | 0o555, implicit: true}
	}
	for name := range u.nodes {
		if name == "." {
			continue
		}
		parent := path.Dir(name)
		for parent != "." && parent != "/" {
			if _, ok := u.nodes[parent]; !ok {
				u.nodes[parent] = &node{
					path:     parent,
					typ:      typeDir,
					mode:     os.ModeDir | 0o555,
					implicit: true,
				}
			}
			parent = path.Dir(parent)
		}
	}

	// 2. Explicit nodes survive only if no higher-layer whiteout/opaque
	// marker hides them. Implicit directories derive visibility from their
	// descendants in the fixpoint below.
	for name := range u.nodes {
		n := u.nodes[name]
		if n.implicit {
			u.visible[name] = false
			continue
		}
		vis, opaqued, err := u.pathVisibility(name)
		if err != nil {
			return err
		}
		u.visible[name] = vis
		// A directory fully hidden by an opaque marker may still need to
		// exist as an implicit container for same/higher-layer children.
		if !vis && opaqued && n.typ == typeDir {
			n.implicit = true
		}
	}
	u.visible["."] = true

	// 3. An implicit directory exists only while it has a visible descendant.
	changed := true
	for changed {
		changed = false
		for name, n := range u.nodes {
			if n.implicit && !u.visible[name] {
				if u.hasVisibleDescendant(name) {
					u.visible[name] = true
					changed = true
				}
			}
		}
	}

	// 4. Bind every hardlink to the safe, visible regular-file object of its
	// layer now that visibility and implicit directories are settled.
	for _, l := range u.layers {
		for name, n := range l.nodes {
			if n.typ != typeHardlink {
				continue
			}
			// If the hardlink node itself was removed by a higher layer, or
			// lost its final-stack visibility, leave it unbound and fail
			// closed if anything still reaches it.
			if winner := u.nodes[name]; winner != n || !u.visible[name] {
				n.linkErr = fmt.Errorf("%w: hardlink %q is hidden in the union", fs.ErrNotExist, name)
				continue
			}
			if err := u.bindHardlink(l, n); err != nil {
				return fmt.Errorf("hardlink %q: %w", name, err)
			}
		}
	}

	// 5. Rebuild the children map from the final visible node set.
	u.children = map[string]map[string]struct{}{}
	for name, vis := range u.visible {
		if !vis || name == "." {
			continue
		}
		parent := path.Dir(name)
		kids, ok := u.children[parent]
		if !ok {
			kids = map[string]struct{}{}
			u.children[parent] = kids
		}
		kids[path.Base(name)] = struct{}{}
	}
	return nil
}

// hasVisibleDescendant reports whether dir contains any visible node below it.
func (u *Union) hasVisibleDescendant(dir string) bool {
	prefix := dir + "/"
	for name, vis := range u.visible {
		if vis && strings.HasPrefix(name, prefix) {
			return true
		}
	}
	return false
}

// hasDescendantWithin reports whether dir has any node contributed by a layer
// at or below limit, independent of final-stack visibility.
func (u *Union) hasDescendantWithin(dir string, limit int) bool {
	prefix := dir + "/"
	for i := 0; i <= limit; i++ {
		for name := range u.layers[i].nodes {
			if strings.HasPrefix(name, prefix) {
				return true
			}
		}
	}
	return false
}

// pathVisibility reports whether the winning node of name survives the
// whiteout/opaque markers of higher layers and, when it is hidden, whether
// the hiding marker was an opaque-directory marker (which allows the path to
// survive as an implicit directory) rather than a plain whiteout.
func (u *Union) pathVisibility(name string) (bool, bool, error) {
	n, ok := u.nodes[name]
	if !ok {
		return false, false, nil
	}
	entryLayer := n.layer
	for i := entryLayer + 1; i < len(u.layers); i++ {
		if _, ok := u.layers[i].whiteouts[name]; ok {
			return false, false, nil
		}
		ancestor := name
		for {
			if _, ok := u.layers[i].whiteouts[ancestor]; ok {
				return false, false, nil
			}
			if _, ok := u.layers[i].opaques[ancestor]; ok {
				return false, true, nil
			}
			if ancestor == "." {
				break
			}
			next := path.Dir(ancestor)
			if next == ancestor {
				break
			}
			ancestor = next
		}
	}
	return true, false, nil
}

// nodeAt returns the effective node of name as seen from layer limit (the
// resolution layer itself included). It applies whiteout/opaque semantics to
// all layers at or below limit and never follows symlinks.
func (u *Union) nodeAt(name string, limit int) (*node, bool, error) {
	entryLayer := -1
	var winner *node
	for i := 0; i <= limit; i++ {
		if n, ok := u.layers[i].nodes[name]; ok {
			winner = n
			entryLayer = i
		}
	}
	if winner == nil {
		// Implicit directories only exist in the synthesized union view.
		if n, ok := u.nodes[name]; ok && n.implicit && n.typ == typeDir && u.visible[name] && u.hasDescendantWithin(name, limit) {
			return n, true, nil
		}
		return nil, false, nil
	}
	vis, err := u.visibleBelow(name, entryLayer, limit)
	if err != nil {
		return nil, false, err
	}
	if !vis {
		return nil, false, nil
	}
	return winner, true, nil
}

func (u *Union) visibleBelow(name string, entryLayer, limit int) (bool, error) {
	for i := entryLayer + 1; i <= limit; i++ {
		if _, ok := u.layers[i].whiteouts[name]; ok {
			return false, nil
		}
		ancestor := name
		for {
			if _, ok := u.layers[i].whiteouts[ancestor]; ok {
				return false, nil
			}
			if _, ok := u.layers[i].opaques[ancestor]; ok {
				return false, nil
			}
			if ancestor == "." {
				break
			}
			next := path.Dir(ancestor)
			if next == ancestor {
				break
			}
			ancestor = next
		}
	}
	return true, nil
}

func (u *Union) bindHardlink(l *Layer, n *node) error {
	target, err := cleanLink(".", n.link)
	if err != nil {
		n.linkErr = err
		return nil
	}
	targetNode, ok, err := u.nodeAt(target, l.idx)
	if err != nil {
		return err
	}
	if !ok || targetNode.typ != typeFile {
		n.linkErr = fmt.Errorf("%w: hardlink target %q is not a visible regular file", fs.ErrNotExist, n.link)
		return nil
	}
	// The target must also survive the full stack: a higher-layer whiteout
	// removes the object the hardlink would alias.
	if !u.visible[target] {
		n.linkErr = fmt.Errorf("%w: hardlink target %q is hidden in the union", fs.ErrNotExist, n.link)
		return nil
	}
	// Every prefix component must be a visible, non-symlink directory so a
	// hardlink cannot be smuggled through a symlinked ancestor.
	ancestor := path.Dir(target)
	for ancestor != "." {
		dirNode, ok, err := u.nodeAt(ancestor, l.idx)
		if err != nil {
			return err
		}
		if !ok || dirNode.typ != typeDir {
			n.linkErr = fmt.Errorf("%w: hardlink target %q crosses a non-directory component", fs.ErrNotExist, n.link)
			return nil
		}
		ancestor = path.Dir(ancestor)
	}
	n.obj = targetNode.obj
	n.mode = targetNode.mode
	n.size = targetNode.size
	return nil
}

// cleanLink resolves a link target against its containing directory (root for
// absolute targets) and rejects any traversal that escapes the union root.
func cleanLink(base, target string) (string, error) {
	if target == "" {
		return "", errors.New("unionfs: empty link target")
	}
	if path.IsAbs(target) {
		depth := 0
		for _, comp := range strings.Split(target, "/") {
			switch comp {
			case "", ".":
			case "..":
				depth--
				if depth < 0 {
					return "", ErrEscapesRoot
				}
			default:
				depth++
			}
		}
		cleaned := path.Clean(target)
		if cleaned == "/.." || strings.HasPrefix(cleaned, "/../") {
			return "", ErrEscapesRoot
		}
		cleaned = strings.TrimPrefix(cleaned, "/")
		if cleaned == "" {
			return ".", nil
		}
		return cleaned, nil
	}

	// Relative links are resolved against the containing directory; count
	// depth relative to that directory so ".." cannot climb past root.
	depth := 0
	for _, comp := range strings.Split(base, "/") {
		if comp != "" && comp != "." {
			depth++
		}
	}
	for _, comp := range strings.Split(target, "/") {
		switch comp {
		case "", ".":
		case "..":
			depth--
			if depth < 0 {
				return "", ErrEscapesRoot
			}
		default:
			depth++
		}
	}
	joined := path.Join("/"+base, target)
	cleaned := path.Clean(joined)
	if cleaned == "/.." || strings.HasPrefix(cleaned, "/../") {
		return "", ErrEscapesRoot
	}
	cleaned = strings.TrimPrefix(cleaned, "/")
	if cleaned == "" {
		return ".", nil
	}
	return cleaned, nil
}

// Open implements fs.FS. It resolves symlinks and rejects links that escape
// the union root.
func (u *Union) Open(name string) (fs.File, error) {
	if err := u.build(); err != nil {
		return nil, err
	}
	n, err := u.lookup(name)
	if err != nil {
		return nil, &fs.PathError{Op: "open", Path: name, Err: err}
	}
	if n.typ == typeHardlink && n.linkErr != nil {
		return nil, &fs.PathError{Op: "open", Path: name, Err: n.linkErr}
	}
	return u.openNode(name, n), nil
}

func (u *Union) openNode(name string, n *node) fs.File {
	switch n.typ {
	case typeDir:
		return &dirHandle{union: u, node: n}
	case typeFile:
		return &fileHandle{node: n, sr: io.NewSectionReader(n.obj.ra, n.obj.offset, n.obj.size)}
	case typeSymlink:
		// Symlinks must be resolved via lookup; reaching here means a symlink
		// ended up as a final component, which is not openable as a file.
		return &symlinkHandle{node: n}
	case typeHardlink:
		if n.linkErr != nil {
			return &errorHandle{node: n, err: n.linkErr}
		}
		return &fileHandle{node: n, sr: io.NewSectionReader(n.obj.ra, n.obj.offset, n.obj.size)}
	default:
		return &errorHandle{node: n, err: fmt.Errorf("unionfs: unsupported file type for %q", name)}
	}
}

// lookup resolves a root-relative path, following intermediate and final
// symlinks, with strict root-boundary checks.
func (u *Union) lookup(name string) (*node, error) {
	rooted, err := cleanLink(".", name)
	if err != nil {
		return nil, err
	}
	components := strings.Split(rooted, "/")
	if len(components) == 1 && components[0] == "." {
		return u.nodes["."], nil
	}

	cur := "."
	hops := 0
	for _, comp := range components {
		cur = path.Join(cur, comp)
		for {
			n, ok := u.nodes[cur]
			if !ok || !u.visible[cur] {
				return nil, fs.ErrNotExist
			}
			if n.typ != typeSymlink {
				if n.typ == typeHardlink && n.linkErr != nil {
					return nil, n.linkErr
				}
				break
			}
			hops++
			if hops > maxSymlinkHops {
				return nil, fs.ErrInvalid
			}
			target, err := cleanLink(path.Dir(cur), n.link)
			if err != nil {
				return nil, err
			}
			cur = target
		}
	}
	return u.nodes[cur], nil
}

// ReadDir implements fs.ReadDirFS.
func (u *Union) ReadDir(name string) ([]fs.DirEntry, error) {
	if err := u.build(); err != nil {
		return nil, err
	}
	n, err := u.lookup(name)
	if err != nil {
		return nil, &fs.PathError{Op: "readdir", Path: name, Err: err}
	}
	if n.typ != typeDir {
		return nil, &fs.PathError{Op: "readdir", Path: name, Err: fs.ErrInvalid}
	}
	kids := u.children[n.path]
	out := make([]fs.DirEntry, 0, len(kids))
	for base := range kids {
		childPath := n.path
		if childPath == "." {
			childPath = base
		} else {
			childPath = path.Join(childPath, base)
		}
		child := u.nodes[childPath]
		out = append(out, &fileInfo{node: child})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name() < out[j].Name() })
	return out, nil
}

// Stat implements fs.StatFS.
func (u *Union) Stat(name string) (fs.FileInfo, error) {
	if err := u.build(); err != nil {
		return nil, err
	}
	n, err := u.lookup(name)
	if err != nil {
		return nil, &fs.PathError{Op: "stat", Path: name, Err: err}
	}
	return &fileInfo{node: n}, nil
}

// ReadFile implements fs.ReadFileFS.
func (u *Union) ReadFile(name string) ([]byte, error) {
	if err := u.build(); err != nil {
		return nil, err
	}
	f, err := u.Open(name)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	ra, ok := f.(io.ReaderAt)
	if !ok {
		return nil, &fs.PathError{Op: "read", Path: name, Err: fs.ErrInvalid}
	}
	n, err := u.lookup(name)
	if err != nil {
		return nil, err
	}
	buf := make([]byte, n.size)
	if _, err := ra.ReadAt(buf, 0); err != nil && !errors.Is(err, io.EOF) {
		return nil, err
	}
	return buf, nil
}
