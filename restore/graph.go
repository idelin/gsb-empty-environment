package restore

// This file resolves staged hard-link references using an inode reference
// graph. Each staged hard link contributes an edge from its own path to its
// target path; targets may themselves be hard links, so resolution walks
// the graph with three-color DFS to detect cycles before publishing any
// inode sharing.

const (
	white = 0 // unvisited
	gray  = 1 // on the current DFS stack
	black = 2 // fully resolved
)

// resolveLinks resolves every staged hard-link reference. On error the
// caller discards the whole staged tree, so partial resolutions here never
// leak into the committed state.
func (st *stagedTree) resolveLinks() error {
	color := map[string]int{}
	// Deterministic order keeps errors and inode IDs reproducible.
	paths := make([]string, 0, len(st.pending))
	for p := range st.pending {
		paths = append(paths, p)
	}
	sortStrings(paths)
	for _, p := range paths {
		if color[p] == white {
			if err := st.resolveOne(p, color); err != nil {
				return err
			}
		}
	}
	return nil
}

// resolveOne walks the reference chain starting at linkPath and returns the
// terminal node after assigning it to every link on the chain.
func (st *stagedTree) resolveOne(linkPath string, color map[string]int) error {
	color[linkPath] = gray
	target, err := cleanPath(st.pending[linkPath])
	if err != nil {
		return &OpError{Path: linkPath, Err: err}
	}

	switch color[target] {
	case gray:
		// The target is a hard link currently being resolved: the chain
		// loops back on itself.
		return &OpError{Path: linkPath, Err: ErrLinkCycle}
	case black:
		// Already resolved; fall through and reuse the terminal node.
	default:
		if _, isLink := st.pending[target]; isLink {
			if err := st.resolveOne(target, color); err != nil {
				return err
			}
		}
	}

	node := st.nodes[target]
	if node == nil {
		return &OpError{Path: linkPath, Err: ErrDanglingLink}
	}
	switch node.Kind {
	case DirNode:
		return &OpError{Path: linkPath, Err: ErrLinkToDir}
	case SymlinkNode:
		// Symbolic links never share an inode: the hard link becomes an
		// independent symbolic link carrying a copy of the link text.
		st.nodes[linkPath] = &Node{Kind: SymlinkNode, Target: node.Target}
	case FileNode:
		node.Inode.Links++
		st.nodes[linkPath] = &Node{Kind: FileNode, Inode: node.Inode}
	}
	color[linkPath] = black
	return nil
}
