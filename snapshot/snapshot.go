// Package snapshot provides a resumable depth-first iterator over a
// versioned tree. Nodes may be shared by multiple parents (a DAG); each
// occurrence is emitted once per path.
//
// An Iterator stops when its node or byte budget is exhausted and yields a
// resume Token. The token binds the tree version and the traversal stack;
// resuming never re-emits nodes, and any tree mutation invalidates old
// tokens.
package snapshot

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// Node is a tree node. The same *Node may appear under several parents;
// it is then emitted once per distinct path.
type Node struct {
	Name     string
	Children []*Node
}

// Tree is a versioned root handle. Every mutation must go through Update
// so the version is bumped and outstanding tokens are invalidated.
type Tree struct {
	root    *Node
	version uint64
}

// NewTree returns a Tree rooted at root (root may be nil).
func NewTree(root *Node) *Tree { return &Tree{root: root} }

// Version returns the current tree version.
func (t *Tree) Version() uint64 { return t.version }

// Update applies fn to the tree and bumps the version, invalidating all
// previously issued tokens.
func (t *Tree) Update(fn func()) {
	fn()
	t.version++
}

// Budget limits one iterator run; both limits are hard caps. A negative
// value means unlimited. A zero budget emits nothing and immediately
// yields a token. For a resumed run to make progress, MaxBytes must fit
// the longest remaining path (and MaxNodes must be non-zero).
type Budget struct {
	MaxNodes int
	MaxBytes int // counts bytes of emitted path strings
}

// Unlimited is a budget with no limits.
var Unlimited = Budget{MaxNodes: -1, MaxBytes: -1}

// ErrStaleToken is returned when a token's version does not match the
// tree's current version.
var ErrStaleToken = errors.New("snapshot: stale resume token")

type frame struct {
	node *Node
	path string
	next int // index of the next child to descend into
}

// Iterator performs a pre-order depth-first traversal.
//
// Invariant between Next calls: the stack is empty (traversal finished) or
// its top frame is the next node to emit and has not been emitted yet.
type Iterator struct {
	version   uint64
	maxNodes  int
	maxBytes  int
	usedNodes int
	usedBytes int
	stack     []frame
}

// NewIterator starts a traversal of t under budget b.
func NewIterator(t *Tree, b Budget) *Iterator {
	it := &Iterator{version: t.version, maxNodes: b.MaxNodes, maxBytes: b.MaxBytes}
	if t.root != nil {
		it.stack = []frame{{node: t.root, path: "/" + t.root.Name}}
	}
	return it
}

// Next returns the path of the next node. ok is false when the traversal
// is finished or the budget is exhausted; call Token to distinguish.
func (it *Iterator) Next() (path string, ok bool) {
	if len(it.stack) == 0 {
		return "", false
	}
	top := &it.stack[len(it.stack)-1]
	if it.maxNodes >= 0 && it.usedNodes+1 > it.maxNodes {
		return "", false
	}
	if it.maxBytes >= 0 && it.usedBytes+len(top.path) > it.maxBytes {
		return "", false
	}
	it.usedNodes++
	it.usedBytes += len(top.path)
	path = top.path
	it.advance()
	return path, true
}

// advance moves the traversal so the new stack top is the next un-emitted
// node, popping fully explored frames.
func (it *Iterator) advance() {
	for len(it.stack) > 0 {
		top := &it.stack[len(it.stack)-1]
		if top.next >= len(top.node.Children) {
			it.stack = it.stack[:len(it.stack)-1]
			continue
		}
		child := top.node.Children[top.next]
		top.next++
		it.stack = append(it.stack, frame{node: child, path: top.path + "/" + child.Name})
		return
	}
}

// Token returns a resume token for the current position. ok is false when
// the traversal is complete and no token is needed.
func (it *Iterator) Token() (token string, ok bool) {
	if len(it.stack) == 0 {
		return "", false
	}
	var sb strings.Builder
	sb.WriteString(strconv.FormatUint(it.version, 16))
	for _, f := range it.stack {
		sb.WriteByte(',')
		sb.WriteString(strconv.Itoa(f.next))
	}
	return sb.String(), true
}

// Resume continues a traversal from a token issued for tree t. It returns
// ErrStaleToken if the tree has changed since the token was issued.
func Resume(t *Tree, token string, b Budget) (*Iterator, error) {
	parts := strings.Split(token, ",")
	version, err := strconv.ParseUint(parts[0], 16, 64)
	if err != nil {
		return nil, fmt.Errorf("snapshot: invalid token: %v", err)
	}
	if version != t.version {
		return nil, ErrStaleToken
	}
	idx := make([]int, len(parts)-1)
	for i, s := range parts[1:] {
		n, err := strconv.Atoi(s)
		if err != nil || n < 0 {
			return nil, fmt.Errorf("snapshot: invalid token")
		}
		idx[i] = n
	}
	it := &Iterator{version: version, maxNodes: b.MaxNodes, maxBytes: b.MaxBytes}
	if t.root == nil {
		if len(idx) != 0 {
			return nil, fmt.Errorf("snapshot: invalid token")
		}
		return it, nil
	}
	if len(idx) == 0 {
		return nil, fmt.Errorf("snapshot: invalid token")
	}
	// Rebuild the stack: frame k+1 is the child its parent descended into,
	// i.e. child index parent.next-1 (parent.next was incremented on push).
	stack := []frame{{node: t.root, path: "/" + t.root.Name, next: idx[0]}}
	for level := 1; level < len(idx); level++ {
		parent := &stack[len(stack)-1]
		ci := parent.next - 1
		if ci < 0 || ci >= len(parent.node.Children) {
			return nil, fmt.Errorf("snapshot: invalid token")
		}
		child := parent.node.Children[ci]
		stack = append(stack, frame{node: child, path: parent.path + "/" + child.Name, next: idx[level]})
	}
	it.stack = stack
	return it, nil
}
