// Package annmerge implements a merge engine for text annotations over a
// collaboratively edited character sequence.
//
// The text is a sequence of characters identified by stable character IDs.
// It is built solely from insert operations that name a predecessor
// (parent) character ID; concurrent inserts sharing the same predecessor
// are ordered by operation ID (descending, so the operation with the
// greater ID sits closer to the shared predecessor). Deletes keep their
// character as a tombstone so it can still serve as an annotation anchor.
//
// Annotations reference a left and a right anchor character ID plus a
// per-endpoint inclusivity flag. Whether a character is covered is decided
// purely by its position in the final anchor sequence and the endpoint
// rules -- never by the position it happened to occupy when some message
// arrived. When several live annotations set the same attribute key on a
// character, the one with the greatest (logicalTime, operationID) wins.
// An undo operation retracts one specific annotation by ID. Deleting a
// character never deletes annotations, including annotations anchored at
// that character.
//
// The operation format defined here is a custom, self-contained protocol;
// it does not claim compatibility with any existing collaborative-editing
// library.
package annmerge

import (
	"fmt"
	"sort"
)

// Sentinel anchor IDs. RootID sorts before every character, EndID after
// every character. Neither is ever visible or deletable.
const (
	RootID = "@root"
	EndID  = "@end"
)

// Kind identifies the operation type.
type Kind string

const (
	KindInsert   Kind = "insert"
	KindDelete   Kind = "delete"
	KindAnnotate Kind = "annotate"
	KindUndo     Kind = "undo"
)

// Op is one operation in the custom wire protocol. Which fields are
// meaningful depends on Kind:
//
//	insert:   ID, Parent (predecessor ID or RootID), Char
//	delete:   ID, Target (character ID; kept as a tombstone anchor)
//	annotate: ID, AnnID, Left, LeftIncl, Right, RightIncl, Attrs, Time
//	undo:     ID, Undo (the AnnID of the annotation to retract)
type Op struct {
	ID   string `json:"id"`
	Kind Kind   `json:"kind"`

	Parent string `json:"parent,omitempty"`
	Char   rune   `json:"char,omitempty"`

	Target string `json:"target,omitempty"`

	AnnID     string            `json:"annID,omitempty"`
	Left      string            `json:"left,omitempty"`
	Right     string            `json:"right,omitempty"`
	LeftIncl  bool              `json:"leftIncl,omitempty"`
	RightIncl bool              `json:"rightIncl,omitempty"`
	Attrs     map[string]string `json:"attrs,omitempty"`
	Time      uint64            `json:"time,omitempty"`

	Undo string `json:"undo,omitempty"`
}

type node struct {
	id       string
	char     rune
	deleted  bool
	children map[string]*node
}

type annotation struct {
	opID      string
	left      string
	right     string
	leftIncl  bool
	rightIncl bool
	attrs     map[string]string
	time      uint64
	undone    bool
}

// Engine holds the merged document state. It is not safe for concurrent
// use; synchronization belongs to the caller's transport layer.
type Engine struct {
	nodes map[string]*node
	root  *node
	end   *node
	anns  map[string]*annotation
}

// New returns an empty engine containing only the two sentinel anchors.
func New() *Engine {
	root := &node{id: RootID, children: map[string]*node{}}
	end := &node{id: EndID}
	return &Engine{
		nodes: map[string]*node{RootID: root, EndID: end},
		root:  root,
		end:   end,
		anns:  map[string]*annotation{},
	}
}

// Apply integrates one operation. It reports false when the operation's
// dependencies (parent character, anchors, or the annotation an undo
// refers to) are not yet known; the caller should buffer the operation
// and retry it once more operations have arrived. Applying an operation
// that is already known is a no-op and reports true.
func (e *Engine) Apply(op Op) bool {
	switch op.Kind {
	case KindInsert:
		if _, dup := e.nodes[op.ID]; dup {
			return true
		}
		parent, ok := e.nodes[op.Parent]
		if !ok {
			return false
		}
		child := &node{id: op.ID, char: op.Char, children: map[string]*node{}}
		parent.children[op.ID] = child
		e.nodes[op.ID] = child
		return true
	case KindDelete:
		n, ok := e.nodes[op.Target]
		if !ok || n == e.root || n == e.end {
			return false
		}
		n.deleted = true
		return true
	case KindAnnotate:
		if _, dup := e.anns[op.AnnID]; dup {
			return true
		}
		if _, ok := e.nodes[op.Left]; !ok {
			return false
		}
		if _, ok := e.nodes[op.Right]; !ok {
			return false
		}
		e.anns[op.AnnID] = &annotation{
			opID:      op.ID,
			left:      op.Left,
			right:     op.Right,
			leftIncl:  op.LeftIncl,
			rightIncl: op.RightIncl,
			attrs:     op.Attrs,
			time:      op.Time,
		}
		return true
	case KindUndo:
		a, ok := e.anns[op.Undo]
		if !ok {
			return false
		}
		a.undone = true
		return true
	}
	return false
}

// ApplyAll applies operations in any delivery order, buffering the ones
// whose dependencies have not arrived yet. It returns an error only if
// some operations can never be satisfied (dangling references).
func (e *Engine) ApplyAll(ops []Op) error {
	pending := append([]Op(nil), ops...)
	for len(pending) > 0 {
		next := make([]Op, 0, len(pending))
		progress := false
		for _, op := range pending {
			if e.Apply(op) {
				progress = true
			} else {
				next = append(next, op)
			}
		}
		if !progress {
			return fmt.Errorf("annmerge: %d ops have unsatisfiable dependencies", len(next))
		}
		pending = next
	}
	return nil
}

// sequence returns every node -- sentinels, live characters and
// tombstones -- in final document order. Children of a node are ordered
// by operation ID descending, so the result is independent of delivery
// order.
func (e *Engine) sequence() []*node {
	seq := make([]*node, 0, len(e.nodes))
	var walk func(n *node)
	walk = func(n *node) {
		seq = append(seq, n)
		kids := make([]*node, 0, len(n.children))
		for _, c := range n.children {
			kids = append(kids, c)
		}
		sort.Slice(kids, func(i, j int) bool { return kids[i].id > kids[j].id })
		for _, c := range kids {
			walk(c)
		}
	}
	walk(e.root)
	seq = append(seq, e.end)
	return seq
}

// CharAttr pairs a visible character with its resolved attributes.
type CharAttr struct {
	ID    string
	Char  rune
	Attrs map[string]string
}

// CharAttrs resolves the winning attributes for every visible character.
// A character is covered by an annotation when its index i in the final
// anchor sequence satisfies:
//
//	i > left  || (leftIncl  && i == left)
//	i < right || (rightIncl && i == right)
//
// Tombstone anchors keep their positions, so deleting an endpoint does
// not move the span. For each attribute key the covering, non-undone
// annotation with the greatest (Time, opID) supplies the value.
func (e *Engine) CharAttrs() []CharAttr {
	seq := e.sequence()
	pos := make(map[string]int, len(seq))
	for i, n := range seq {
		pos[n.id] = i
	}
	var out []CharAttr
	for i, n := range seq {
		if n == e.root || n == e.end || n.deleted {
			continue
		}
		attrs := map[string]string{}
		bestTime := map[string]uint64{}
		bestOp := map[string]string{}
		for _, a := range e.anns {
			if a.undone {
				continue
			}
			li, ri := pos[a.left], pos[a.right]
			if !(i > li || (a.leftIncl && i == li)) {
				continue
			}
			if !(i < ri || (a.rightIncl && i == ri)) {
				continue
			}
			for k, v := range a.attrs {
				curT, ok := bestTime[k]
				curOp := bestOp[k]
				if !ok || a.time > curT || (a.time == curT && a.opID > curOp) {
					bestTime[k] = a.time
					bestOp[k] = a.opID
					attrs[k] = v
				}
			}
		}
		out = append(out, CharAttr{ID: n.id, Char: n.char, Attrs: attrs})
	}
	return out
}

// Text returns the visible document text.
func (e *Engine) Text() string {
	var b []rune
	for _, ca := range e.CharAttrs() {
		b = append(b, ca.Char)
	}
	return string(b)
}

// Run is a maximal span of consecutive characters sharing identical
// attributes.
type Run struct {
	Text  string
	Attrs map[string]string
}

// Runs returns the minimal list of runs covering the visible text.
func (e *Engine) Runs() []Run {
	var runs []Run
	for _, ca := range e.CharAttrs() {
		if len(runs) > 0 && attrsEqual(runs[len(runs)-1].Attrs, ca.Attrs) {
			runs[len(runs)-1].Text += string(ca.Char)
			continue
		}
		attrs := make(map[string]string, len(ca.Attrs))
		for k, v := range ca.Attrs {
			attrs[k] = v
		}
		runs = append(runs, Run{Text: string(ca.Char), Attrs: attrs})
	}
	return runs
}

func attrsEqual(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if bv, ok := b[k]; !ok || bv != v {
			return false
		}
	}
	return true
}
