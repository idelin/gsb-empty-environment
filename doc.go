// Package anno implements a small collaborative-text core built on stable
// character IDs.
//
// The text sequence is assembled from insert operations that reference a
// predecessor character ID (RGA-style). Concurrent inserts sharing the same
// predecessor are ordered by operation ID, higher ID first. Deletes keep the
// character as a tombstone so it remains usable as an annotation anchor.
//
// Annotations reference left/right anchor character IDs with independent
// endpoint inclusivity. Whether a character is covered is decided by its
// position in the final anchor sequence, never by the position it happened
// to occupy when some message arrived.
//
// The operation set and any encoding of it are a custom, self-contained
// protocol; no compatibility with any existing collaboration library is
// claimed.
package anno

import (
	"sort"
	"strings"
)

// ID is a stable, totally ordered identifier for an operation and, for
// inserts, for the character the insert creates. Ordering is (Time, Peer).
type ID struct {
	Time int    // Lamport logical time
	Peer string // replica ID, tiebreak for concurrent operations
}

// Root is the virtual anchor before the first character. It is a valid
// insert predecessor and a valid annotation anchor, but it never produces a
// visible character. Real operations must use Time >= 0.
var Root = ID{Time: -1, Peer: ""}

// Less reports whether a orders before b.
func (a ID) Less(b ID) bool {
	if a.Time != b.Time {
		return a.Time < b.Time
	}
	return a.Peer < b.Peer
}

// Op is any document operation. Operations may be delivered in any order,
// including before the operations they reference; Doc buffers them until
// their dependencies have arrived.
type Op interface{ opID() ID }

// Insert appends a character immediately after Pred in the sequence.
type Insert struct {
	ID   ID // becomes the stable character ID
	Pred ID // predecessor character, Root for the document start
	Char rune
}

func (op Insert) opID() ID { return op.ID }

// Delete tombstones a character. The anchor is retained, and annotations
// covering other characters are unaffected.
type Delete struct {
	ID     ID
	Target ID // character (insert) ID to tombstone
}

func (op Delete) opID() ID { return op.ID }

// Annotate attaches one attribute to the characters between two anchors.
// Among overlapping annotations with the same Key, the one with the maximum
// (logical time, operation ID) wins.
type Annotate struct {
	ID        ID // orders same-key overlaps: max (Time, Peer) wins
	Ann       string
	Key, Val  string
	Left      ID // left anchor character, Root allowed
	LeftIncl  bool
	Right     ID // right anchor character, Root allowed
	RightIncl bool
}

func (op Annotate) opID() ID { return op.ID }

// Undo deactivates one specific annotation, referenced by its Ann ID.
type Undo struct {
	ID  ID
	Ann string
}

func (op Undo) opID() ID { return op.ID }

// Doc is a replica of the collaborative document. It is not safe for
// concurrent use.
type Doc struct {
	chars    map[ID]rune
	children map[ID][]ID // predecessor -> children, ordered by ID descending
	deleted  map[ID]bool
	anns     map[string]Annotate
	undone   map[string]bool
	pending  []Op
}

// New returns an empty document.
func New() *Doc {
	return &Doc{
		chars:    map[ID]rune{},
		children: map[ID][]ID{},
		deleted:  map[ID]bool{},
		anns:     map[string]Annotate{},
		undone:   map[string]bool{},
	}
}

// Apply merges one operation, regardless of delivery order. Operations whose
// dependencies have not arrived yet are buffered and retried after every
// subsequent Apply.
func (d *Doc) Apply(op Op) {
	d.pending = append(d.pending, op)
	d.flush()
}

// Pending reports how many operations are still waiting for dependencies.
func (d *Doc) Pending() int { return len(d.pending) }

func (d *Doc) flush() {
	for {
		progressed := false
		kept := d.pending[:0]
		for _, op := range d.pending {
			if d.tryApply(op) {
				progressed = true
			} else {
				kept = append(kept, op)
			}
		}
		d.pending = kept
		if !progressed {
			return
		}
	}
}

func (d *Doc) tryApply(op Op) bool {
	switch op := op.(type) {
	case Insert:
		if _, dup := d.chars[op.ID]; dup {
			return true
		}
		if !d.hasChar(op.Pred) {
			return false
		}
		d.chars[op.ID] = op.Char
		d.children[op.Pred] = insertChild(d.children[op.Pred], op.ID)
		return true
	case Delete:
		if _, ok := d.chars[op.Target]; !ok {
			return false
		}
		d.deleted[op.Target] = true
		return true
	case Annotate:
		if _, dup := d.anns[op.Ann]; dup {
			return true
		}
		if !d.hasChar(op.Left) || !d.hasChar(op.Right) {
			return false
		}
		d.anns[op.Ann] = op
		return true
	case Undo:
		if _, ok := d.anns[op.Ann]; !ok {
			return false
		}
		d.undone[op.Ann] = true
		return true
	}
	return true
}

func (d *Doc) hasChar(id ID) bool {
	if id == Root {
		return true
	}
	_, ok := d.chars[id]
	return ok
}

// insertChild inserts id into a slice sorted by ID descending: concurrent
// inserts with the same predecessor are ordered by operation ID, higher ID
// (newer operation) first.
func insertChild(s []ID, id ID) []ID {
	i := sort.Search(len(s), func(i int) bool { return s[i].Less(id) })
	s = append(s, ID{})
	copy(s[i+1:], s[i:])
	s[i] = id
	return s
}

// sequence returns every character ID in final document order, tombstones
// included, preceded by the virtual Root anchor. Annotation coverage is
// evaluated against this sequence.
func (d *Doc) sequence() []ID {
	seq := []ID{Root}
	var walk func(pred ID)
	walk = func(pred ID) {
		for _, c := range d.children[pred] {
			seq = append(seq, c)
			walk(c)
		}
	}
	walk(Root)
	return seq
}

// Text returns the visible document text, tombstones omitted.
func (d *Doc) Text() string {
	var b strings.Builder
	for _, id := range d.sequence() {
		if id == Root || d.deleted[id] {
			continue
		}
		b.WriteRune(d.chars[id])
	}
	return b.String()
}
