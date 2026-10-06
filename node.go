// Package layermerge merges ordered virtual directory layers into a single
// tree, following overlay-style semantics: whiteout entries remove lower
// nodes, opaque directories hide lower children, upper nodes replace lower
// nodes of a different type, and hard links resolve only within their own
// layer.
package layermerge

import (
	"fmt"
	"sort"
	"strings"
)

const (
	// WhiteoutPrefix marks an entry that deletes the same-named lower node.
	WhiteoutPrefix = ".wh."
	// OpaqueMarker marks the containing directory as opaque.
	OpaqueMarker = ".wh..wh..opq"
)

// Kind is the node type.
type Kind int

const (
	KindFile Kind = iota
	KindDir
	KindSymlink
	KindHardlink
)

func (k Kind) String() string {
	switch k {
	case KindFile:
		return "file"
	case KindDir:
		return "dir"
	case KindSymlink:
		return "symlink"
	case KindHardlink:
		return "hardlink"
	}
	return "unknown"
}

// Node is a single entry in a layer or merged tree.
type Node struct {
	Name     string
	Kind     Kind
	Content  string           // file content
	Target   string           // symlink / hardlink target (layer-absolute path)
	Children map[string]*Node // directories only
	Layer    int              // source layer index; -1 before merge

	// Resolved points at the hardlink target inside the node's own layer.
	// It is never resolved against other layers.
	Resolved *Node
}

// IsWhiteout reports whether name is a whiteout entry (not the opaque marker).
func IsWhiteout(name string) bool {
	return strings.HasPrefix(name, WhiteoutPrefix) && name != OpaqueMarker
}

// WhiteoutTarget returns the lower node name a whiteout entry removes.
func WhiteoutTarget(name string) string {
	return strings.TrimPrefix(name, WhiteoutPrefix)
}

// Dir creates a directory node with the given children.
func Dir(name string, children ...*Node) *Node {
	n := &Node{Name: name, Kind: KindDir, Layer: -1, Children: map[string]*Node{}}
	for _, c := range children {
		n.Children[c.Name] = c
	}
	return n
}

// File creates a regular file node.
func File(name, content string) *Node {
	return &Node{Name: name, Kind: KindFile, Content: content, Layer: -1}
}

// Symlink creates a symbolic link node.
func Symlink(name, target string) *Node {
	return &Node{Name: name, Kind: KindSymlink, Target: target, Layer: -1}
}

// Hardlink creates a hard link node; target is resolved within its own layer.
func Hardlink(name, target string) *Node {
	return &Node{Name: name, Kind: KindHardlink, Target: target, Layer: -1}
}

// Whiteout creates a whiteout entry removing the lower node named target.
func Whiteout(target string) *Node {
	return &Node{Name: WhiteoutPrefix + target, Kind: KindFile, Layer: -1}
}

// Opaque creates the marker making the containing directory opaque.
func Opaque() *Node {
	return &Node{Name: OpaqueMarker, Kind: KindFile, Layer: -1}
}

// lookup walks a layer-absolute path ("/a/b") from n and returns the node.
func (n *Node) lookup(path string) *Node {
	cur := n
	for _, part := range strings.Split(strings.Trim(path, "/"), "/") {
		if part == "" {
			continue
		}
		if cur.Kind != KindDir {
			return nil
		}
		next, ok := cur.Children[part]
		if !ok {
			return nil
		}
		cur = next
	}
	return cur
}

// resolveHardlinks binds every hardlink in the subtree to its target inside
// the same subtree. Dangling targets are reported as errors.
func (n *Node) resolveHardlinks(layerName string) error {
	var errs []string
	var walk func(cur *Node, path string)
	walk = func(cur *Node, path string) {
		if cur.Kind == KindHardlink {
			t := n.lookup(cur.Target)
			if t == nil || t.Kind == KindDir {
				errs = append(errs, fmt.Sprintf("layer %q: hardlink %s -> %s: target not found in layer", layerName, path, cur.Target))
			} else {
				cur.Resolved = t
			}
		}
		if cur.Kind == KindDir {
			for name, c := range cur.Children {
				walk(c, path+"/"+name)
			}
		}
	}
	walk(n, "")
	if len(errs) > 0 {
		return fmt.Errorf("%s", strings.Join(errs, "; "))
	}
	return nil
}

// sortedNames returns child names in deterministic order.
func (n *Node) sortedNames() []string {
	names := make([]string, 0, len(n.Children))
	for name := range n.Children {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
