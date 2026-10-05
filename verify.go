package main

import (
	"fmt"
	"strings"
)

// FlatModel is an independent verification model. Unlike Tree (a pointer
// graph), it stores the whole state as a flat path -> identity table and
// implements every operation with pure string manipulation. Agreement
// between the two models after replaying the same log is strong evidence
// the replay is correct.
type FlatModel struct {
	// path -> (id, isDir); "/" always present.
	cells map[string]flatCell
}

type flatCell struct {
	id  int64
	dir bool
}

func NewFlatModel() *FlatModel {
	return &FlatModel{cells: map[string]flatCell{"/": {id: 0, dir: true}}}
}

func parentOf(p string) string {
	if p == "/" {
		return ""
	}
	i := strings.LastIndex(p, "/")
	if i <= 0 {
		return "/"
	}
	return p[:i]
}

func baseOf(p string) string {
	i := strings.LastIndex(p, "/")
	return p[i+1:]
}

func (m *FlatModel) has(p string) bool {
	_, ok := m.cells[p]
	return ok
}

func (m *FlatModel) parentExists(p string) bool {
	par := parentOf(p)
	c, ok := m.cells[par]
	return ok && c.dir
}

// childrenOf lists direct children paths of dir path p.
func (m *FlatModel) childrenOf(p string) []string {
	var out []string
	prefix := p
	if prefix != "/" {
		prefix += "/"
	} else {
		prefix = "/"
	}
	for k := range m.cells {
		if k == p {
			continue
		}
		if strings.HasPrefix(k, prefix) && parentOf(k) == p {
			out = append(out, k)
		}
	}
	return out
}

func (m *FlatModel) subtree(p string) []string {
	var out []string
	prefix := p + "/"
	for k := range m.cells {
		if k == p || strings.HasPrefix(k, prefix) {
			out = append(out, k)
		}
	}
	return out
}

func (m *FlatModel) Create(p string, id int64, dir bool) error {
	if p == "/" {
		return fmt.Errorf("create: cannot create root")
	}
	for _, c := range m.cells {
		if c.id == id {
			return fmt.Errorf("create %q: id %d already in use", p, id)
		}
	}
	if !m.parentExists(p) {
		return fmt.Errorf("create %q: parent missing or not a directory", p)
	}
	if m.has(p) {
		return fmt.Errorf("create %q: name already exists", p)
	}
	m.cells[p] = flatCell{id: id, dir: dir}
	return nil
}

func (m *FlatModel) Unlink(p string) error {
	c, ok := m.cells[p]
	if !ok {
		return fmt.Errorf("unlink %q: does not exist", p)
	}
	if p == "/" {
		return fmt.Errorf("unlink: cannot remove root")
	}
	if c.dir && len(m.childrenOf(p)) > 0 {
		return fmt.Errorf("unlink %q: directory not empty", p)
	}
	delete(m.cells, p)
	return nil
}

func (m *FlatModel) Rename(oldPath, newPath string) error {
	src, ok := m.cells[oldPath]
	if !ok {
		return fmt.Errorf("rename %q: does not exist", oldPath)
	}
	if oldPath == "/" {
		return fmt.Errorf("rename: cannot move root")
	}
	if !m.parentExists(newPath) {
		return fmt.Errorf("rename %q -> %q: target parent missing", oldPath, newPath)
	}
	if oldPath == newPath {
		return nil
	}
	// Cycle check on pure strings: new location must not sit inside the
	// moved subtree.
	if src.dir && (newPath == oldPath || strings.HasPrefix(parentOf(newPath)+"/", oldPath+"/")) {
		return fmt.Errorf("rename %q -> %q: would create a parent cycle", oldPath, newPath)
	}
	// Stage the whole change, then commit: nothing mutates before every
	// rule has passed, so the operation is atomic.
	staged := map[string]flatCell{}
	for k, v := range m.cells {
		staged[k] = v
	}
	if dst, exists := staged[newPath]; exists {
		switch {
		case dst.dir && !src.dir:
			return fmt.Errorf("rename %q -> %q: cannot replace directory with file", oldPath, newPath)
		case !dst.dir && src.dir:
			return fmt.Errorf("rename %q -> %q: cannot replace file with directory", oldPath, newPath)
		case dst.dir && len(m.childrenOf(newPath)) > 0:
			return fmt.Errorf("rename %q -> %q: target directory not empty", oldPath, newPath)
		}
		for _, k := range m.subtree(newPath) {
			delete(staged, k)
		}
	}
	for _, k := range m.subtree(oldPath) {
		v := staged[k]
		delete(staged, k)
		staged[newPath+k[len(oldPath):]] = v
	}
	m.cells = staged
	return nil
}

// Mapping returns the path -> id view for comparison with Tree.Mapping.
func (m *FlatModel) Mapping() map[string]int64 {
	out := make(map[string]int64, len(m.cells))
	for p, c := range m.cells {
		out[p] = c.id
	}
	return out
}

// Verify replays the log through the independent FlatModel and compares
// the resulting mapping against the tree-based replay result. It returns
// a list of mismatches (empty means the models agree).
func Verify(entries []Entry, treeMapping map[string]int64) []string {
	fm := NewFlatModel()
	var problems []string
	for _, e := range entries {
		var err error
		switch e.Op {
		case "create":
			err = fm.Create(e.Path, e.ID, e.Dir)
		case "unlink":
			err = fm.Unlink(e.Path)
		case "rename":
			err = fm.Rename(e.Old, e.New)
		default:
			err = fmt.Errorf("unknown op %q", e.Op)
		}
		_ = err // failures must match the tree model's; final mapping decides
	}
	flat := fm.Mapping()
	for p, id := range flat {
		tid, ok := treeMapping[p]
		if !ok {
			problems = append(problems, fmt.Sprintf("path %q (#%d) present in flat model, missing in tree", p, id))
		} else if tid != id {
			problems = append(problems, fmt.Sprintf("path %q: id mismatch flat=#%d tree=#%d", p, id, tid))
		}
	}
	for p, id := range treeMapping {
		if _, ok := flat[p]; !ok {
			problems = append(problems, fmt.Sprintf("path %q (#%d) present in tree, missing in flat model", p, id))
		}
	}
	return problems
}
