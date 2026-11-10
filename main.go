package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

type kind int

const (
	kDir kind = iota
	kFile
	kLink
)

type node struct {
	kind   kind
	abs    bool     // link target is absolute
	target []string // link target components
	kids   map[string]*node
}

func newNode(k kind) *node {
	return &node{kind: k, kids: map[string]*node{}}
}

type rerr struct{ msg string }

func (e *rerr) Error() string { return e.msg }

var (
	errEscape  = &rerr{"path escapes virtual root"}
	errTooMany = &rerr{"more than 40 symlink jumps in one operation"}
	errAbsLink = &rerr{"absolute symlink target followed"}
	errNotDir  = &rerr{"intermediate component is not a directory"}
	errDirFile = &rerr{"cannot write regular file over directory"}
	errBadLeaf = &rerr{"cannot replace existing non-symlink leaf"}
	errBadOp   = &rerr{"unknown operation"}
)

// deque stores path components still to be resolved.
type deque struct {
	s    []string
	head int
}

func (d *deque) len() int { return len(d.s) - d.head }

func (d *deque) pop() string {
	x := d.s[d.head]
	d.head++
	if d.head == len(d.s) {
		d.s, d.head = d.s[:0], 0
	}
	return x
}

func (d *deque) prepend(xs []string) {
	if d.head > 0 {
		d.s = d.s[d.head:]
		d.head = 0
	}
	d.s = append(append([]string{}, xs...), d.s...)
}

func isAbs(p string) bool { return strings.HasPrefix(p, "/") }

// components splits a virtual path, dropping empty segments. "." is kept
// because it is a real no-op component that must be honored.
func components(p string) []string {
	var out []string
	for _, c := range strings.Split(p, "/") {
		if c != "" {
			out = append(out, c)
		}
	}
	return out
}

// resolveDir walks comps which must all resolve to directories, implicitly
// creating missing directories. Symlinks are expanded inline; the returned
// node is the final directory.
func resolveDir(root *node, comps []string) (*node, error) {
	stack := []*node{root}
	dq := deque{s: append([]string{}, comps...)}
	jumps := 0

	for dq.len() > 0 {
		cur := stack[len(stack)-1]
		c := dq.pop()

		switch c {
		case ".":
			continue
		case "..":
			if len(stack) == 1 {
				return nil, errEscape
			}
			stack = stack[:len(stack)-1]
			continue
		}

		child := cur.kids[c]
		switch {
		case child == nil:
			dir := newNode(kDir) // implicit parent directory
			cur.kids[c] = dir
			stack = append(stack, dir)
		case child.kind == kDir:
			stack = append(stack, child)
		case child.kind == kFile:
			return nil, errNotDir
		default: // symlink: replace in place, target resolved from current dir
			jumps++
			if jumps > 40 {
				return nil, errTooMany
			}
			if child.abs {
				return nil, errAbsLink
			}
			dq.prepend(child.target)
		}
	}
	return stack[len(stack)-1], nil
}

// doF writes a regular file, following the leaf component when it is a link.
func doF(root *node, comps []string) error {
	stack := []*node{root}
	dq := deque{s: append([]string{}, comps...)}
	jumps := 0

	for dq.len() > 0 {
		cur := stack[len(stack)-1]
		c := dq.pop()

		switch c {
		case ".":
			continue
		case "..":
			if len(stack) == 1 {
				return errEscape
			}
			stack = stack[:len(stack)-1]
			continue
		}

		child := cur.kids[c]
		last := dq.len() == 0
		switch {
		case child == nil:
			if last {
				cur.kids[c] = newNode(kFile) // create the written file
				return nil
			}
			dir := newNode(kDir) // implicit parent directory
			cur.kids[c] = dir
			stack = append(stack, dir)
		case child.kind == kFile:
			if !last {
				return errNotDir
			}
			return nil // overwrite existing regular file
		case child.kind == kDir:
			if last {
				return errDirFile
			}
			stack = append(stack, child)
		default: // symlink: F always follows it, including the leaf component
			jumps++
			if jumps > 40 {
				return errTooMany
			}
			if child.abs {
				return errAbsLink
			}
			dq.prepend(child.target)
		}
	}
	return errDirFile // empty path resolves to the root directory
}

// doL creates or replaces a leaf symlink. It never follows the leaf itself;
// only parent-directory components are resolved (following links there).
func doL(root *node, comps []string, target string) error {
	if len(comps) == 0 {
		return errBadLeaf
	}
	leaf := comps[len(comps)-1]
	if leaf == "." || leaf == ".." {
		return errBadLeaf
	}
	parent, err := resolveDir(root, comps[:len(comps)-1])
	if err != nil {
		return err
	}
	if existing := parent.kids[leaf]; existing != nil && existing.kind != kLink {
		return errBadLeaf
	}
	parent.kids[leaf] = &node{
		kind:   kLink,
		abs:    isAbs(target),
		target: components(target),
		kids:   map[string]*node{},
	}
	return nil
}

func main() {
	var raw [][]json.RawMessage
	if err := json.NewDecoder(os.Stdin).Decode(&raw); err != nil {
		fmt.Println(-1)
		return
	}

	root := newNode(kDir)

	for i, op := range raw {
		fail := func() { fmt.Println(i) }

		var typ, p string
		if len(op) < 2 || json.Unmarshal(op[0], &typ) != nil || json.Unmarshal(op[1], &p) != nil {
			fail()
			return
		}
		if isAbs(p) { // absolute write path is rejected
			fail()
			return
		}

		var err error
		switch typ {
		case "F":
			err = doF(root, components(p))
		case "L":
			var target string
			if len(op) < 3 || json.Unmarshal(op[2], &target) != nil {
				err = errBadOp
			} else {
				err = doL(root, components(p), target)
			}
		default:
			err = errBadOp
		}

		if err != nil {
			if os.Getenv("GSB_DEBUG") != "" {
				fmt.Fprintf(os.Stderr, "op %d (%v) rejected: %v\n", i, op, err)
			}
			fail()
			return
		}
	}
	fmt.Println(-1)
}
