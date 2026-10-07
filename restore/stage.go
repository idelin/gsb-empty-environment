package restore

import "sort"

func sortStrings(s []string) { sort.Strings(s) }

// stagedTree is a copy-on-write working copy of the committed tree. It is
// discarded wholesale if the batch fails, so a failed Apply never publishes
// a half-built tree.
type stagedTree struct {
	nodes     map[string]*Node
	inodes    map[int]*Inode
	nextInode int
	pending   map[string]string // hard-link path -> raw target path
}

func (t *Tree) stage() *stagedTree {
	st := &stagedTree{
		nodes:     make(map[string]*Node, len(t.nodes)),
		inodes:    make(map[int]*Inode, len(t.inodes)),
		pending:   map[string]string{},
		nextInode: 1,
	}
	for p, n := range t.nodes {
		st.nodes[p] = n // nodes are immutable once committed
	}
	for id, ino := range t.inodes {
		cp := *ino // copy: link counts may grow during staging
		st.inodes[id] = &cp
		if id >= st.nextInode {
			st.nextInode = id + 1
		}
	}
	// Re-point file nodes at the copied inodes so committed state is never
	// mutated through a shared pointer.
	for p, n := range st.nodes {
		if n.Kind == FileNode {
			st.nodes[p] = &Node{Kind: FileNode, Inode: st.inodes[n.Inode.ID]}
		}
	}
	return st
}

func (st *stagedTree) add(e Entry) error {
	p, err := cleanPath(e.Path)
	if err != nil {
		return &OpError{Path: e.Path, Err: err}
	}
	if _, dup := st.nodes[p]; dup {
		return &OpError{Path: p, Err: ErrDuplicatePath}
	}
	if err := st.checkParents(p); err != nil {
		return &OpError{Path: p, Err: err}
	}
	switch e.Type {
	case FileEntry:
		ino := &Inode{ID: st.nextInode, Content: append([]byte(nil), e.Content...), Links: 1}
		st.nextInode++
		st.inodes[ino.ID] = ino
		st.nodes[p] = &Node{Kind: FileNode, Inode: ino}
	case DirEntry:
		st.nodes[p] = &Node{Kind: DirNode}
	case SymlinkEntry:
		if e.Target == "" {
			return &OpError{Path: p, Err: ErrMalformedEntry}
		}
		st.nodes[p] = &Node{Kind: SymlinkNode, Target: e.Target}
	case HardlinkEntry:
		if e.Target == "" {
			return &OpError{Path: p, Err: ErrMalformedEntry}
		}
		// Stage the reference only; the target may not exist yet.
		st.nodes[p] = &Node{Kind: FileNode} // placeholder, resolved later
		st.pending[p] = e.Target
	default:
		return &OpError{Path: p, Err: ErrMalformedEntry}
	}
	return nil
}

// checkParents verifies every existing ancestor of p is a directory.
// Missing ancestors are created implicitly as directories.
func (st *stagedTree) checkParents(p string) error {
	for parent := parentOf(p); parent != ""; parent = parentOf(parent) {
		n, ok := st.nodes[parent]
		if !ok {
			st.nodes[parent] = &Node{Kind: DirNode}
			continue
		}
		if n.Kind != DirNode {
			return ErrParentNotDir
		}
		return nil // everything above an existing dir already exists
	}
	return nil
}

func parentOf(p string) string {
	for i := len(p) - 1; i >= 0; i-- {
		if p[i] == '/' {
			return p[:i]
		}
	}
	return ""
}

func (st *stagedTree) freeze() *Tree {
	return &Tree{nodes: st.nodes, inodes: st.inodes}
}
