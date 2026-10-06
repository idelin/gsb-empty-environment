package fsdiff

import (
	"fmt"
	"sort"
	"strings"
)

// vnode is one inode in the virtual tree; hard links appear as multiple paths.
type vnode struct {
	isDir bool
	hash  string
	paths map[string]bool
}

// VTree is a virtual inode tree used to apply a diff to a source snapshot
// and verify that the result reproduces the target snapshot exactly.
type VTree struct {
	nodes  map[uint64]*vnode // by inode
	byPath map[string]uint64 // path -> inode
	tmpSeq int
}

// NewVTree seeds a virtual tree from a snapshot.
func NewVTree(s *Snapshot) *VTree {
	t := &VTree{nodes: map[uint64]*vnode{}, byPath: map[string]uint64{}}
	for ino, paths := range s.Inodes {
		n := &vnode{isDir: s.Entries[paths[0]].IsDir, hash: s.Entries[paths[0]].Hash, paths: map[string]bool{}}
		for _, p := range paths {
			n.paths[p] = true
			t.byPath[p] = ino
		}
		t.nodes[ino] = n
	}
	return t
}

// Apply plays ops in order. Moves are resolved by inode, so crossing moves
// (a->b, b->a) apply cleanly: a destination still held by another inode sends
// that inode to a temporary slot until its own op places it. Any leftover
// temporary path makes the final Snapshot mismatch the target.
func (t *VTree) Apply(ops []Op) error {
	for i, op := range ops {
		if err := t.applyOne(op); err != nil {
			return fmt.Errorf("op %d (%s %s): %w", i, op.Kind, op.Path, err)
		}
	}
	return nil
}

func (t *VTree) applyOne(op Op) error {
	switch op.Kind {
	case OpAdd:
		if _, ok := t.nodes[op.Inode]; ok {
			return fmt.Errorf("inode %d already exists", op.Inode)
		}
		if _, ok := t.byPath[op.Path]; ok {
			return fmt.Errorf("path %q occupied", op.Path)
		}
		t.nodes[op.Inode] = &vnode{isDir: op.IsDir, hash: op.NewHash, paths: map[string]bool{op.Path: true}}
		t.byPath[op.Path] = op.Inode

	case OpRemove:
		n, ok := t.nodes[op.Inode]
		if !ok {
			return fmt.Errorf("unknown inode %d", op.Inode)
		}
		if !n.paths[op.Path] {
			return fmt.Errorf("inode %d not at %q", op.Inode, op.Path)
		}
		delete(n.paths, op.Path)
		delete(t.byPath, op.Path)
		if len(n.paths) == 0 {
			delete(t.nodes, op.Inode)
		}

	case OpMove:
		n, ok := t.nodes[op.Inode]
		if !ok {
			return fmt.Errorf("unknown inode %d", op.Inode)
		}
		if len(n.paths) != 1 {
			return fmt.Errorf("inode %d has %d paths, want exactly 1 for move", op.Inode, len(n.paths))
		}
		var oldPath string
		for p := range n.paths {
			oldPath = p
			delete(t.byPath, p)
			delete(n.paths, p)
		}
		t.evict(op.Path)
		n.paths[op.Path] = true
		t.byPath[op.Path] = op.Inode
		if op.IsDir && oldPath != op.Path {
			t.moveSubtree(oldPath, op.Path)
		}

	case OpModify:
		n, ok := t.nodes[op.Inode]
		if !ok {
			return fmt.Errorf("unknown inode %d", op.Inode)
		}
		n.hash = op.NewHash

	case OpLinkAdd:
		n, ok := t.nodes[op.Inode]
		if !ok {
			return fmt.Errorf("unknown inode %d", op.Inode)
		}
		if _, ok := t.byPath[op.Path]; ok {
			return fmt.Errorf("path %q occupied", op.Path)
		}
		n.paths[op.Path] = true
		t.byPath[op.Path] = op.Inode

	case OpLinkRemove:
		n, ok := t.nodes[op.Inode]
		if !ok {
			return fmt.Errorf("unknown inode %d", op.Inode)
		}
		if !n.paths[op.Path] {
			return fmt.Errorf("inode %d not at %q", op.Inode, op.Path)
		}
		delete(n.paths, op.Path)
		delete(t.byPath, op.Path)
		if len(n.paths) == 0 {
			delete(t.nodes, op.Inode)
		}
	}
	return nil
}

// moveSubtree rewrites every path beneath a moved directory, across all
// inodes, so a directory move carries its whole subtree with it.
func (t *VTree) moveSubtree(oldDir, newDir string) {
	prefix := oldDir + "/"
	var rewrites [][2]string
	for p := range t.byPath {
		if np := newDir + p[len(oldDir):]; strings.HasPrefix(p, prefix) && np != p {
			rewrites = append(rewrites, [2]string{p, np})
		}
	}
	for _, rw := range rewrites {
		ino := t.byPath[rw[0]]
		t.evict(rw[1])
		delete(t.byPath, rw[0])
		n := t.nodes[ino]
		delete(n.paths, rw[0])
		n.paths[rw[1]] = true
		t.byPath[rw[1]] = ino
	}
}

// evict relocates whatever inode currently holds path to a temporary slot,
// returning the slot used ("" if the path was free).
func (t *VTree) evict(path string) string {
	ino, ok := t.byPath[path]
	if !ok {
		return ""
	}
	t.tmpSeq++
	tmp := fmt.Sprintf(".vtree-tmp-%d", t.tmpSeq)
	n := t.nodes[ino]
	delete(n.paths, path)
	delete(t.byPath, path)
	n.paths[tmp] = true
	t.byPath[tmp] = ino
	return tmp
}

// Snapshot reconstructs the tree as a snapshot for comparison with a target.
func (t *VTree) Snapshot() *Snapshot {
	s := &Snapshot{Entries: map[string]Entry{}, Inodes: map[uint64][]string{}}
	for ino, n := range t.nodes {
		paths := make([]string, 0, len(n.paths))
		for p := range n.paths {
			paths = append(paths, p)
		}
		sort.Strings(paths)
		s.Inodes[ino] = paths
		for _, p := range paths {
			s.Entries[p] = Entry{Path: p, Inode: ino, IsDir: n.isDir, Hash: n.hash}
		}
	}
	return s
}
