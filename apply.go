package fsdiff

import (
	"fmt"
	"path"
	"sort"
)

// VTree is a mutable virtual inode tree used to replay a diff against
// the source snapshot and verify that the result equals the target.
// All structure is keyed by inode ID, so ancestor renames never
// invalidate pending operations.
type VTree struct {
	root    ID
	inodes  map[ID]*Inode
	entries map[ID]map[string]ID
	nlinks  map[ID]int // number of directory entries pointing at each inode
}

// NewVTree deep-copies a Snapshot into a mutable VTree.
func NewVTree(s *Snapshot) *VTree {
	t := &VTree{
		root:    s.Root,
		inodes:  make(map[ID]*Inode, len(s.Inodes)),
		entries: make(map[ID]map[string]ID, len(s.Entries)),
		nlinks:  make(map[ID]int, len(s.Inodes)),
	}
	for id, ino := range s.Inodes {
		cp := *ino
		cp.Paths = append([]string(nil), ino.Paths...)
		t.inodes[id] = &cp
	}
	for id, dir := range s.Entries {
		cp := make(map[string]ID, len(dir))
		for name, child := range dir {
			cp[name] = child
			t.nlinks[child]++
		}
		t.entries[id] = cp
	}
	t.nlinks[s.Root] = 1
	return t
}

// Apply replays ops on the tree. Removals are applied deepest-first,
// then additions shallowest-first, then content modifications; this
// makes cross moves (a<->b swaps) safe without temporary names.
func (t *VTree) Apply(ops []Op) error {
	var removals, additions, modifies []Op
	for _, op := range ops {
		switch op.Kind {
		case OpDelete, OpUnlink, OpMove:
			removals = append(removals, op)
		}
		if op.Kind == OpCreate || op.Kind == OpLink || op.Kind == OpMove {
			additions = append(additions, op)
		}
		if op.Kind == OpModify {
			modifies = append(modifies, op)
		}
	}
	sort.SliceStable(removals, func(i, j int) bool {
		return depth(removals[i].removePath()) > depth(removals[j].removePath())
	})
	sort.SliceStable(additions, func(i, j int) bool {
		return depth(additions[i].addPath()) < depth(additions[j].addPath())
	})

	for _, op := range removals {
		parent, name := op.FromParent, path.Base(op.From)
		if op.Kind != OpMove {
			parent, name = op.Parent, path.Base(op.Path)
		}
		if err := t.removeName(parent, name); err != nil {
			return fmt.Errorf("fsdiff: apply %v: %w", op, err)
		}
		if op.Kind == OpDelete && t.nlinks[op.ID] == 0 {
			delete(t.nlinks, op.ID)
			delete(t.inodes, op.ID)
			delete(t.entries, op.ID)
		}
	}
	for _, op := range additions {
		parent, name := op.Parent, path.Base(op.To)
		if op.Kind != OpMove {
			name = path.Base(op.Path)
		}
		if op.Kind == OpCreate {
			if _, exists := t.inodes[op.ID]; exists {
				return fmt.Errorf("fsdiff: apply %v: inode already exists", op)
			}
			cp := *op.Node
			t.inodes[op.ID] = &cp
			if op.Node.Kind == KindDir {
				t.entries[op.ID] = make(map[string]ID)
			}
		}
		if err := t.addName(parent, name, op.ID); err != nil {
			return fmt.Errorf("fsdiff: apply %v: %w", op, err)
		}
	}
	for _, op := range modifies {
		ino, ok := t.inodes[op.ID]
		if !ok {
			return fmt.Errorf("fsdiff: apply %v: unknown inode", op)
		}
		ino.Kind = op.Node.Kind
		ino.Mode = op.Node.Mode
		ino.Digest = op.Node.Digest
	}
	return nil
}

func (op Op) removePath() string {
	if op.Kind == OpMove {
		return op.From
	}
	return op.Path
}

func (op Op) addPath() string {
	if op.Kind == OpMove {
		return op.To
	}
	return op.Path
}

func (t *VTree) removeName(parent ID, name string) error {
	dir, ok := t.entries[parent]
	if !ok {
		return fmt.Errorf("parent inode %+v is not a directory", parent)
	}
	id, ok := dir[name]
	if !ok {
		return fmt.Errorf("no entry %q in parent %+v", name, parent)
	}
	delete(dir, name)
	t.nlinks[id]--
	return nil
}

func (t *VTree) addName(parent ID, name string, id ID) error {
	dir, ok := t.entries[parent]
	if !ok {
		return fmt.Errorf("parent inode %+v is not a directory", parent)
	}
	if _, occupied := dir[name]; occupied {
		return fmt.Errorf("entry %q in parent %+v is occupied", name, parent)
	}
	dir[name] = id
	t.nlinks[id]++
	return nil
}

// paths computes the current path set of every inode by walking the
// tree from the root.
func (t *VTree) paths() map[ID][]string {
	computed := make(map[ID][]string, len(t.inodes))
	computed[t.root] = []string{""}
	var walk func(dir ID, prefix string)
	walk = func(dir ID, prefix string) {
		names := make([]string, 0, len(t.entries[dir]))
		for name := range t.entries[dir] {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			child := t.entries[dir][name]
			p := name
			if prefix != "" {
				p = prefix + "/" + name
			}
			computed[child] = append(computed[child], p)
			if t.inodes[child].Kind == KindDir {
				walk(child, p)
			}
		}
	}
	walk(t.root, "")
	return computed
}

// Equal reports whether the tree matches the snapshot exactly:
// same root, same inode set with equal state, same path sets, and
// same directory entries.
func (t *VTree) Equal(s *Snapshot) error {
	if t.root != s.Root {
		return fmt.Errorf("root mismatch: tree %+v, snapshot %+v", t.root, s.Root)
	}
	if len(t.inodes) != len(s.Inodes) {
		return fmt.Errorf("inode count mismatch: tree %d, snapshot %d", len(t.inodes), len(s.Inodes))
	}
	paths := t.paths()
	for id, want := range s.Inodes {
		got, ok := t.inodes[id]
		if !ok {
			return fmt.Errorf("missing inode %+v (paths %v)", id, want.Paths)
		}
		if got.Kind != want.Kind || got.Mode != want.Mode || got.Digest != want.Digest {
			return fmt.Errorf("inode %+v state mismatch: tree {%v %v %v}, snapshot {%v %v %v}",
				id, got.Kind, got.Mode, got.Digest, want.Kind, want.Mode, want.Digest)
		}
		gotPaths := paths[id]
		if len(gotPaths) != len(want.Paths) {
			return fmt.Errorf("inode %+v path set mismatch: tree %v, snapshot %v", id, gotPaths, want.Paths)
		}
		for i, p := range want.Paths {
			if gotPaths[i] != p {
				return fmt.Errorf("inode %+v path set mismatch: tree %v, snapshot %v", id, gotPaths, want.Paths)
			}
		}
		if want.Kind == KindDir {
			gotDir, wantDir := t.entries[id], s.Entries[id]
			if len(gotDir) != len(wantDir) {
				return fmt.Errorf("dir %+v entries mismatch: tree %v, snapshot %v", id, gotDir, wantDir)
			}
			for name, child := range wantDir {
				if gotDir[name] != child {
					return fmt.Errorf("dir %+v entry %q: tree %+v, snapshot %+v", id, name, gotDir[name], child)
				}
			}
		}
	}
	return nil
}

// Verify replays ops on a virtual copy of old and checks that the
// result is identical to new.
func Verify(old, new *Snapshot, ops []Op) error {
	t := NewVTree(old)
	if err := t.Apply(ops); err != nil {
		return err
	}
	return t.Equal(new)
}
