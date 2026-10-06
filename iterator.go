package snapiter

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// ErrStaleToken is returned by Resume when the token was issued for a
// different tree or for an older version of the tree (i.e. the tree was
// mutated after the token was handed out).
var ErrStaleToken = errors.New("snapiter: stale token: tree changed since token was issued")

// ErrInvalidToken is returned by Resume when the token is malformed or does
// not describe a valid position in the current tree.
var ErrInvalidToken = errors.New("snapiter: invalid resume token")

// Entry is one emitted node together with the path (child indices from the
// root) at which it was reached. A shared node appears once per path.
type Entry struct {
	Path []int
	Node *Node
}

// frame is one level of the traversal stack: a node plus the index of the
// next child to descend into.
type frame struct {
	node *Node
	path []int
	next int
}

// Iterator performs a deterministic pre-order depth-first traversal of a
// tree snapshot. It is not safe for concurrent use.
type Iterator struct {
	tree     *Tree
	version  uint64
	stack    []frame
	lastPath []int
	started  bool
	done     bool
}

// NewIterator starts a fresh traversal of the tree's current version.
func (t *Tree) NewIterator() *Iterator {
	return &Iterator{tree: t, version: t.version}
}

// Resume rebuilds an iterator from a token previously returned by Next.
// It fails with ErrStaleToken if the tree was mutated after the token was
// issued, and with ErrInvalidToken if the token is malformed.
func (t *Tree) Resume(token string) (*Iterator, error) {
	started, path, err := t.parseToken(token)
	if err != nil {
		return nil, err
	}
	it := &Iterator{tree: t, version: t.version, started: started}
	if !started {
		return it, nil
	}
	if t.root == nil {
		return nil, ErrInvalidToken
	}
	// Rebuild the traversal stack from the path of the last emitted node:
	// every ancestor resumes after the child that leads down the path, and
	// the last emitted node resumes at its first child.
	node := t.root
	it.stack = append(it.stack, frame{node: node, path: nil})
	for depth, idx := range path {
		if idx < 0 || idx >= len(node.Children) {
			return nil, ErrInvalidToken
		}
		it.stack[depth].next = idx + 1
		childPath := append(append([]int(nil), it.stack[depth].path...), idx)
		node = node.Children[idx]
		it.stack = append(it.stack, frame{node: node, path: childPath})
	}
	it.lastPath = append([]int(nil), path...)
	return it, nil
}

// Next emits nodes until the node budget (maxNodes) or the byte budget
// (maxBytes, charged as len(ID)+len(Data) per node) is exhausted.
//
// To guarantee progress, a call with maxNodes > 0 always emits at least one
// node when any remain, even if that node alone exceeds maxBytes. A call
// with maxNodes <= 0 emits nothing and only reports the current position.
//
// When the traversal is incomplete the returned token is non-empty and can
// be passed to Tree.Resume to continue exactly where this call stopped; no
// node is emitted twice. When the traversal is finished done is true and
// the token is empty.
func (it *Iterator) Next(maxNodes, maxBytes int) (entries []Entry, token string, done bool) {
	if it.done {
		return nil, "", true
	}
	if maxBytes < 0 {
		maxBytes = 0
	}
	used := 0
	for len(entries) < maxNodes {
		next, ok := it.peek()
		if !ok {
			it.done = true
			break
		}
		if len(entries) > 0 && used+next.size() > maxBytes {
			break
		}
		node, path, _ := it.advance()
		entries = append(entries, Entry{Path: path, Node: node})
		used += node.size()
	}
	if !it.done {
		// The budget may have been exhausted exactly at the end of the
		// traversal; check whether anything remains so the caller gets
		// done=true instead of a token that leads nowhere.
		if _, ok := it.peek(); !ok {
			it.done = true
		}
	}
	if it.done {
		return entries, "", true
	}
	return entries, it.token(), false
}

// token encodes the iterator position: tree id, version, and the path of
// the last emitted node (empty when nothing has been emitted yet).
func (it *Iterator) token() string {
	var b strings.Builder
	b.WriteString("snap1.")
	b.WriteString(it.tree.id)
	b.WriteByte('.')
	b.WriteString(strconv.FormatUint(it.version, 10))
	b.WriteByte('.')
	if !it.started {
		b.WriteString("0.")
		return b.String()
	}
	b.WriteString("1.")
	for i, idx := range it.lastPath {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(strconv.Itoa(idx))
	}
	return b.String()
}

func (t *Tree) parseToken(token string) (started bool, path []int, err error) {
	parts := strings.Split(token, ".")
	if len(parts) != 5 || parts[0] != "snap1" {
		return false, nil, ErrInvalidToken
	}
	if parts[1] != t.id {
		return false, nil, ErrStaleToken
	}
	version, err := strconv.ParseUint(parts[2], 10, 64)
	if err != nil {
		return false, nil, ErrInvalidToken
	}
	if version != t.version {
		return false, nil, ErrStaleToken
	}
	switch parts[3] {
	case "0":
		return false, nil, nil
	case "1":
	default:
		return false, nil, ErrInvalidToken
	}
	if parts[4] == "" {
		return true, nil, nil
	}
	for _, s := range strings.Split(parts[4], ",") {
		idx, err := strconv.Atoi(s)
		if err != nil || idx < 0 {
			return false, nil, ErrInvalidToken
		}
		path = append(path, idx)
	}
	return true, path, nil
}

// advance emits the next node in pre-order and updates the stack.
func (it *Iterator) advance() (*Node, []int, bool) {
	if !it.started {
		if it.tree.root == nil {
			return nil, nil, false
		}
		it.started = true
		it.stack = []frame{{node: it.tree.root}}
		it.lastPath = nil
		return it.tree.root, nil, true
	}
	for len(it.stack) > 0 {
		top := &it.stack[len(it.stack)-1]
		if top.next < len(top.node.Children) {
			child := top.node.Children[top.next]
			childPath := append(append([]int(nil), top.path...), top.next)
			top.next++
			it.stack = append(it.stack, frame{node: child, path: childPath})
			it.lastPath = childPath
			return child, childPath, true
		}
		it.stack = it.stack[:len(it.stack)-1]
	}
	return nil, nil, false
}

// peek returns the node advance would emit next, without moving the
// iterator. It runs advance on a shallow copy of the stack.
func (it *Iterator) peek() (*Node, bool) {
	cp := *it
	cp.stack = append([]frame(nil), it.stack...)
	node, _, ok := cp.advance()
	return node, ok
}

// String describes the iterator position for debugging.
func (it *Iterator) String() string {
	return fmt.Sprintf("Iterator(version=%d started=%v done=%v depth=%d)",
		it.version, it.started, it.done, len(it.stack))
}
