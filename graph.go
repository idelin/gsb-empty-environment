package readproof

import (
	"fmt"
	"sort"
)

type nodeInfo struct {
	node Node
	size uint64
	// ends[i] is the cumulative logical end of Children[i].
	ends []uint64
}

type graph struct {
	source NodeSource
	info   map[Digest]*nodeInfo
}

func newGraph(source NodeSource) *graph {
	return &graph{
		source: source,
		info:   make(map[Digest]*nodeInfo),
	}
}

// get loads and authenticates one node.  It deliberately does not descend
// into children: callers only need to load nodes on paths that intersect the
// requested read, which keeps partial manifests and huge shared trees useful.
func (g *graph) get(id Digest) (*nodeInfo, error) {
	if info, ok := g.info[id]; ok {
		return info, nil
	}
	node, ok := g.source.Node(id)
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrMissingNode, id)
	}
	if node.Kind == InternalNode {
		for _, child := range node.Children {
			if child.Digest == id {
				return nil, fmt.Errorf("%w: self reference at node %s", ErrCycle, id)
			}
		}
	}
	computed, err := node.Digest()
	if err != nil {
		return nil, err
	}
	if computed != id {
		return nil, fmt.Errorf("%w: claimed %s, computed %s", ErrIdentityMismatch, id, computed)
	}

	switch node.Kind {
	case LeafNode:
		if len(node.Children) != 0 {
			return nil, fmt.Errorf("%w: leaf %s has children", ErrInvalidNode, id)
		}
		info := &nodeInfo{node: node, size: node.Leaf.Length}
		g.info[id] = info
		return info, nil

	case InternalNode:
		if uint64(len(node.Children)) > (^uint64(0)-9)/(32+8+8) {
			return nil, fmt.Errorf("%w: internal node %s has too many children", ErrLengthOverflow, id)
		}

		ends := make([]uint64, len(node.Children))
		var total uint64
		for i, child := range node.Children {
			childEnd := child.Offset + child.Length
			if childEnd < child.Offset {
				return nil, fmt.Errorf("%w: child interval of %s", ErrLengthOverflow, id)
			}
			if total > ^uint64(0)-child.Length {
				return nil, fmt.Errorf("%w: logical size of %s", ErrLengthOverflow, id)
			}
			total += child.Length
			ends[i] = total
		}
		info := &nodeInfo{node: node, size: total, ends: ends}
		g.info[id] = info
		return info, nil

	default:
		return nil, fmt.Errorf("%w: unknown node kind %d", ErrInvalidNode, node.Kind)
	}
}

func (g *graph) infoFor(id Digest) (*nodeInfo, error) {
	return g.get(id)
}

func checkChildBounds(child Child, info *nodeInfo) error {
	end := child.Offset + child.Length
	if end < child.Offset {
		return fmt.Errorf("%w: child interval", ErrLengthOverflow)
	}
	if end > info.size {
		return fmt.Errorf("%w: child %s interval [%d,%d) exceeds size %d",
			ErrInvalidNode, child.Digest, child.Offset, end, info.size)
	}
	return nil
}

// childAt returns the child interval containing local.  A zero-length child is
// never selected.  The caller must ensure local < info.size.
func (info *nodeInfo) childAt(local uint64) (Child, uint64, uint64, bool) {
	if len(info.ends) == 0 {
		return Child{}, 0, 0, false
	}
	i := sort.Search(len(info.ends), func(i int) bool { return info.ends[i] > local })
	if i == len(info.ends) {
		return Child{}, 0, 0, false
	}
	var start uint64
	if i > 0 {
		start = info.ends[i-1]
	}
	return info.node.Children[i], start, info.ends[i], true
}
