package layermerge

import "fmt"

// Layer is one ordered directory tree. Index 0 is the bottom layer.
type Layer struct {
	Name string
	Root *Node // must be KindDir
}

// NewLayer builds a layer from root children and resolves its hardlinks
// against its own tree only.
func NewLayer(name string, children ...*Node) (*Layer, error) {
	root := Dir("/", children...)
	l := &Layer{Name: name, Root: root}
	if err := root.resolveHardlinks(name); err != nil {
		return nil, err
	}
	return l, nil
}

// MustLayer is NewLayer that panics on error, for tests and demos.
func MustLayer(name string, children ...*Node) *Layer {
	l, err := NewLayer(name, children...)
	if err != nil {
		panic(err)
	}
	return l
}

// Snapshot is the cumulative merged tree after applying layers[0..Layer].
type Snapshot struct {
	Layer int
	Root  *Node
}

// Merge overlays layers in order (layers[0] is the bottom) and returns the
// final tree. Every node's Layer field records the layer that introduced it.
func Merge(layers ...*Layer) (*Node, error) {
	snaps, err := Expand(layers...)
	if err != nil {
		return nil, err
	}
	if len(snaps) == 0 {
		return Dir("/"), nil
	}
	return snaps[len(snaps)-1].Root, nil
}

// Expand applies layers one at a time and returns the cumulative tree after
// each step, so whiteouts and type replacements can be verified per layer.
func Expand(layers ...*Layer) ([]*Snapshot, error) {
	snaps := make([]*Snapshot, 0, len(layers))
	cur := Dir("/")
	for i, l := range layers {
		if l.Root == nil || l.Root.Kind != KindDir {
			return nil, fmt.Errorf("layer %d (%q): root must be a directory", i, l.Name)
		}
		next := clone(cur)
		overlay(next, l.Root, i)
		snaps = append(snaps, &Snapshot{Layer: i, Root: next})
		cur = next
	}
	return snaps, nil
}

// overlay applies src (layer index li) on top of dst in place.
func overlay(dst, src *Node, li int) {
	// Pass 1: an opaque marker hides all lower children of this directory.
	if _, ok := src.Children[OpaqueMarker]; ok {
		dst.Children = map[string]*Node{}
	}
	// Pass 2: whiteouts delete lower nodes; everything else is copied up.
	for _, name := range src.sortedNames() {
		child := src.Children[name]
		switch {
		case name == OpaqueMarker:
			// consumed above
		case IsWhiteout(name):
			delete(dst.Children, WhiteoutTarget(name))
		case child.Kind == KindDir:
			if existing, ok := dst.Children[name]; ok && existing.Kind == KindDir {
				overlay(existing, child, li) // dir over dir merges
			} else {
				dst.Children[name] = cloneFrom(child, li) // dir replaces non-dir
			}
		default:
			dst.Children[name] = cloneFrom(child, li) // type replacement
		}
	}
}

// clone deep-copies n, preserving the recorded source layer.
func clone(n *Node) *Node {
	c := &Node{
		Name:     n.Name,
		Kind:     n.Kind,
		Content:  n.Content,
		Target:   n.Target,
		Layer:    n.Layer,
		Resolved: n.Resolved,
	}
	if n.Children != nil {
		c.Children = make(map[string]*Node, len(n.Children))
		for name, child := range n.Children {
			c.Children[name] = clone(child)
		}
	}
	return c
}

// cloneFrom deep-copies n, stamping every node with source layer li.
// Hardlink Resolved pointers keep pointing into the original layer tree,
// which is exactly the layer-local resolution semantics.
func cloneFrom(n *Node, li int) *Node {
	c := &Node{
		Name:     n.Name,
		Kind:     n.Kind,
		Content:  n.Content,
		Target:   n.Target,
		Layer:    li,
		Resolved: n.Resolved,
	}
	if n.Children != nil {
		c.Children = make(map[string]*Node, len(n.Children))
		for name, child := range n.Children {
			c.Children[name] = cloneFrom(child, li)
		}
	}
	return c
}
