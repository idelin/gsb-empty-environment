package main

import (
	"fmt"
	"strings"
)

// modelNode is a flat record. The independent model deliberately avoids
// child pointers: children are discovered by scanning, and paths are
// derived by walking parents, so an implementation bug in Tree is
// unlikely to be mirrored here.
type modelNode struct {
	parent uint64
	name   string
	dir    bool
	alive  bool
}

// Model is the independent checker tree. It re-executes every log entry
// and its resulting path -> identity mapping must equal Tree's mapping
// after every step.
type Model struct {
	nodes map[uint64]*modelNode
	next  uint64
}

// NewModel returns a model holding only the root directory (id 0).
func NewModel() *Model {
	return &Model{
		nodes: map[uint64]*modelNode{0: {parent: 0, name: "", dir: true, alive: true}},
		next:  1,
	}
}

func (m *Model) child(parent uint64, name string) (uint64, bool) {
	for id, n := range m.nodes {
		if n.alive && n.parent == parent && n.name == name && id != 0 {
			return id, true
		}
	}
	return 0, false
}

func (m *Model) hasChildren(id uint64) bool {
	for cid, n := range m.nodes {
		if cid != id && n.alive && n.parent == id {
			return true
		}
	}
	return false
}

func (m *Model) resolve(p string) (uint64, error) {
	parts, err := splitPath(p)
	if err != nil {
		return 0, err
	}
	cur := uint64(0)
	for _, part := range parts {
		if !m.nodes[cur].dir {
			return 0, fmt.Errorf("path %q: %q is not a directory", p, m.nodes[cur].name)
		}
		next, ok := m.child(cur, part)
		if !ok {
			return 0, fmt.Errorf("path %q: component %q does not exist", p, part)
		}
		cur = next
	}
	return cur, nil
}

// Create mirrors Tree.Create.
func (m *Model) Create(p string, dir bool) (uint64, error) {
	parentPath, name, err := splitParent(p)
	if err != nil {
		return 0, err
	}
	pid, err := m.resolve(parentPath)
	if err != nil {
		return 0, err
	}
	if !m.nodes[pid].dir {
		return 0, fmt.Errorf("create %q: parent is not a directory", p)
	}
	if _, ok := m.child(pid, name); ok {
		return 0, fmt.Errorf("create %q: name already exists", p)
	}
	id := m.next
	m.next++
	m.nodes[id] = &modelNode{parent: pid, name: name, dir: dir, alive: true}
	return id, nil
}

// Unlink mirrors Tree.Unlink.
func (m *Model) Unlink(p string) (uint64, error) {
	id, err := m.resolve(p)
	if err != nil {
		return 0, err
	}
	if id == 0 {
		return 0, fmt.Errorf("unlink %q: cannot unlink the root", p)
	}
	if m.nodes[id].dir && m.hasChildren(id) {
		return 0, fmt.Errorf("unlink %q: directory not empty", p)
	}
	m.nodes[id].alive = false
	return id, nil
}

// Rename mirrors Tree.Rename, including cycle prevention and
// target replacement, with validation fully before the commit point.
func (m *Model) Rename(src, dst string) (moved uint64, replaced uint64, hasReplaced bool, err error) {
	sid, err := m.resolve(src)
	if err != nil {
		return 0, 0, false, err
	}
	if sid == 0 {
		return 0, 0, false, fmt.Errorf("rename %q: cannot rename the root", src)
	}
	dstParentPath, dstName, err := splitParent(dst)
	if err != nil {
		return 0, 0, false, err
	}
	dpid, err := m.resolve(dstParentPath)
	if err != nil {
		return 0, 0, false, err
	}
	if !m.nodes[dpid].dir {
		return 0, 0, false, fmt.Errorf("rename %q -> %q: destination parent is not a directory", src, dst)
	}
	if m.nodes[sid].dir {
		for cur := dpid; ; cur = m.nodes[cur].parent {
			if cur == sid {
				return 0, 0, false, fmt.Errorf("rename %q -> %q: would create a parent cycle", src, dst)
			}
			if cur == 0 {
				break
			}
		}
	}
	tid, occupied := m.child(dpid, dstName)
	if occupied && tid != sid {
		tn, sn := m.nodes[tid], m.nodes[sid]
		if tn.dir != sn.dir {
			return 0, 0, false, fmt.Errorf("rename %q -> %q: cannot replace %s with %s",
				src, dst, kindOf(tn.dir), kindOf(sn.dir))
		}
		if tn.dir && m.hasChildren(tid) {
			return 0, 0, false, fmt.Errorf("rename %q -> %q: target directory not empty", src, dst)
		}
	}
	// commit point
	if occupied {
		if tid == sid {
			return sid, 0, false, nil
		}
		m.nodes[tid].alive = false
		replaced, hasReplaced = tid, true
	}
	sn := m.nodes[sid]
	sn.parent = dpid
	sn.name = dstName
	return sid, replaced, hasReplaced, nil
}

func (m *Model) pathOf(id uint64) string {
	if id == 0 {
		return "/"
	}
	var parts []string
	for cur := id; cur != 0; cur = m.nodes[cur].parent {
		parts = append(parts, m.nodes[cur].name)
	}
	for i, j := 0, len(parts)-1; i < j; i, j = i+1, j-1 {
		parts[i], parts[j] = parts[j], parts[i]
	}
	return "/" + strings.Join(parts, "/")
}

// Mapping returns the model's path -> identity snapshot.
func (m *Model) Mapping() map[string]NodeID {
	out := map[string]NodeID{}
	for id, n := range m.nodes {
		if n.alive {
			out[m.pathOf(id)] = NodeID(id)
		}
	}
	return out
}

// Checker audits the log itself: duplicate sequence numbers, sequence
// gaps, and delete-then-recreate at the same path (which must yield a
// fresh identity, never a reused one).
type Checker struct {
	seen        map[int]bool
	last        int
	started     bool
	unlinked    map[string]NodeID
	Issues      []string
	Recreations []string
}

// NewChecker returns a ready-to-use log checker.
func NewChecker() *Checker {
	return &Checker{seen: map[int]bool{}, unlinked: map[string]NodeID{}}
}

// CheckSeq flags duplicate and gapped sequence numbers.
func (c *Checker) CheckSeq(seq int) {
	if c.seen[seq] {
		c.Issues = append(c.Issues, fmt.Sprintf("duplicate sequence number %d", seq))
	} else if c.started {
		switch {
		case seq > c.last+1:
			c.Issues = append(c.Issues, fmt.Sprintf("sequence gap: %d follows %d", seq, c.last))
		case seq <= c.last:
			c.Issues = append(c.Issues, fmt.Sprintf("out-of-order sequence number %d after %d", seq, c.last))
		}
	}
	c.seen[seq] = true
	c.started = true
	if seq > c.last {
		c.last = seq
	}
}

// NoteUnlink records that path held id just before removal.
func (c *Checker) NoteUnlink(path string, id NodeID) {
	c.unlinked[path] = id
}

// NoteCreate records a creation; if the path was previously unlinked,
// the new identity must differ from the old one.
func (c *Checker) NoteCreate(path string, id NodeID) {
	old, ok := c.unlinked[path]
	if !ok {
		return
	}
	delete(c.unlinked, path)
	if old == id {
		c.Issues = append(c.Issues,
			fmt.Sprintf("identity reuse at %q: recreated with same id=%d after unlink", path, id))
		return
	}
	c.Recreations = append(c.Recreations,
		fmt.Sprintf("path %q recreated after unlink: old id=%d, new id=%d", path, old, id))
}
