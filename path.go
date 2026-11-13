package netsec

import "fmt"

// treeGeometry describes a heap-indexed binary tree.  The root has index 0,
// the children of node i are 2*i+1 and 2*i+2, and leaves occupy
// [2^h-1, 2^(h+1)-2].
type treeGeometry struct {
	height int
	leaves int
	nodes  int
}

func newTreeGeometry(height int) treeGeometry {
	leaves := 1 << height
	return treeGeometry{
		height: height,
		leaves: leaves,
		nodes:  2*leaves - 1,
	}
}

func (t treeGeometry) leafNode(leaf int) (int, error) {
	if leaf < 0 || leaf >= t.leaves {
		return 0, fmt.Errorf("%w: leaf %d outside [0,%d)", ErrInvalidConfig, leaf, t.leaves)
	}
	return t.leaves - 1 + leaf, nil
}

// pathRootToLeaf returns the nodes of one leaf path in root-to-leaf order.
func (t treeGeometry) pathRootToLeaf(leaf int) ([]int, error) {
	node, err := t.leafNode(leaf)
	if err != nil {
		return nil, err
	}
	path := make([]int, 0, t.height+1)
	for {
		path = append(path, node)
		if node == 0 {
			break
		}
		node = (node - 1) / 2
	}
	for i, j := 0, len(path)-1; i < j; i, j = i+1, j-1 {
		path[i], path[j] = path[j], path[i]
	}
	return path, nil
}

// pathLeafToRoot is the fixed eviction/write-back order.
func (t treeGeometry) pathLeafToRoot(leaf int) ([]int, error) {
	path, err := t.pathRootToLeaf(leaf)
	if err != nil {
		return nil, err
	}
	for i, j := 0, len(path)-1; i < j; i, j = i+1, j-1 {
		path[i], path[j] = path[j], path[i]
	}
	return path, nil
}

// isAncestor reports whether ancestor is on the root-to-descendant path.
func (t treeGeometry) isAncestor(ancestor, descendant int) bool {
	if ancestor < 0 || descendant < 0 || ancestor >= t.nodes || descendant >= t.nodes {
		return false
	}
	for {
		if descendant == ancestor {
			return true
		}
		if descendant == 0 {
			return false
		}
		descendant = (descendant - 1) / 2
	}
}
