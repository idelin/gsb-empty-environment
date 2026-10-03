// Package peritext implements a Peritext/Fugue-style CRDT for rich-text
// formatting spans (bold, italic, underline, links, ...) over a
// collaboratively edited text sequence.
//
// Text is a Fugue-lite list CRDT: every character is a node in a tree
// (insert-after parent pointer) and sibling order is resolved by character
// ID, so concurrent inserts converge deterministically on every replica.
//
// Formatting follows Peritext: a format mark is an operation anchored to
// character IDs (never to numeric offsets), so concurrent edits cannot
// corrupt span boundaries. Conflicting add/remove of the same attribute is
// resolved by causality:
//
//   - A remove cancels exactly the add operations it has causally seen
//     (recorded in RemoveMark.Cancels). Concurrent adds are unaffected, so
//     a concurrent "bold" vs "unbold" deterministically keeps the bold.
//   - Among concurrent adds of the same attribute covering a character,
//     the one with the highest Lamport timestamp wins (ties broken by
//     actor ID), e.g. two conflicting hyperlinks on one range.
//
// All rules are deterministic functions of the operation set, and Doc
// state is a grow-only union of operations, so Merge is associative,
// commutative and idempotent: replicas that have seen the same operations
// compute identical text and identical formatting.
package peritext

import (
	"fmt"
	"sort"
	"strings"
)

// ActorID uniquely identifies a replica / user.
type ActorID string

// ID is a unique, immutable identifier for a character or a mark
// operation. Seq is a per-actor Lamport-style counter.
type ID struct {
	Actor ActorID
	Seq   int
}

// IsZero reports whether id is the zero ID, used as the doc-start
// sentinel parent/anchor.
func (id ID) IsZero() bool { return id.Actor == "" && id.Seq == 0 }

// comesBefore orders sibling characters that share the same parent:
// higher Seq first, ties broken by higher Actor. Deterministic everywhere.
func (id ID) comesBefore(o ID) bool {
	if id.Seq != o.Seq {
		return id.Seq > o.Seq
	}
	return id.Actor > o.Actor
}

func (id ID) String() string { return fmt.Sprintf("%s:%d", id.Actor, id.Seq) }

// Anchor is one boundary of a format span. It points at a character ID, so
// it stays stable no matter how the surrounding text is edited. The zero
// anchor denotes a document edge: the start of the document, or the end
// when IsEnd is set.
type Anchor struct {
	Char  ID
	IsEnd bool
}

var (
	// DocStart anchors a span boundary to the very beginning of the text.
	DocStart = Anchor{}
	// DocEnd anchors a span boundary to the very end of the text.
	DocEnd = Anchor{IsEnd: true}
)

// After returns an anchor positioned immediately after the character id.
func After(id ID) Anchor { return Anchor{Char: id} }

// char is one character in the Fugue-lite sequence.
type char struct {
	id     ID
	parent ID // insert-after parent; zero = beginning of the document
	value  rune
}

// AddMark is an operation applying Attr=Value (e.g. "bold"="true" or
// "link"="https://...") to every character in the half-open span
// (Start, End].
type AddMark struct {
	ID    ID
	Attr  string
	Value string
	Start Anchor
	End   Anchor
	Ts    int // Lamport timestamp, for deterministic conflict resolution
}

// RemoveMark is an operation removing Attr from every character in
// (Start, End]. It cancels exactly the adds listed in Cancels — those the
// remover had causally seen. Concurrent adds are unaffected.
type RemoveMark struct {
	ID      ID
	Attr    string
	Start   Anchor
	End     Anchor
	Cancels []ID
}

// Doc is a replicated rich-text document.
type Doc struct {
	actor ActorID
	clock int // Lamport clock
	chars map[ID]char
	adds  map[ID]AddMark
	rems  map[ID]RemoveMark

	order []ID // cached character order
	dirty bool
}

// New creates an empty document replica owned by actor.
func New(actor ActorID) *Doc {
	return &Doc{
		actor: actor,
		chars: make(map[ID]char),
		adds:  make(map[ID]AddMark),
		rems:  make(map[ID]RemoveMark),
		dirty: true,
	}
}

// Fork returns an independent replica of d owned by actor, sharing all of
// d's current state. Use it to simulate a second collaborator.
func (d *Doc) Fork(actor ActorID) *Doc {
	f := New(actor)
	f.clock = d.clock
	for id, c := range d.chars {
		f.chars[id] = c
	}
	for id, m := range d.adds {
		f.adds[id] = m
	}
	for id, r := range d.rems {
		f.rems[id] = r
	}
	return f
}

// nextID allocates a fresh ID and advances the Lamport clock.
func (d *Doc) nextID() ID {
	d.clock++
	return ID{Actor: d.actor, Seq: d.clock}
}

// observe advances the Lamport clock past a seen timestamp.
func (d *Doc) observe(seq int) {
	if seq > d.clock {
		d.clock = seq
	}
}

// Insert inserts text immediately after the character with ID after
// (zero ID = beginning of the document) and returns the new character IDs
// in order.
func (d *Doc) Insert(after ID, text string) []ID {
	parent := after
	ids := make([]ID, 0, len(text))
	for _, r := range text {
		id := d.nextID()
		d.chars[id] = char{id: id, parent: parent, value: r}
		ids = append(ids, id)
		parent = id
	}
	d.dirty = true
	return ids
}

// AddMarkRange applies Attr=Value to the span (start, end].
func (d *Doc) AddMarkRange(attr, value string, start, end Anchor) ID {
	id := d.nextID()
	d.adds[id] = AddMark{ID: id, Attr: attr, Value: value, Start: start, End: end, Ts: d.clock}
	return id
}

// RemoveMarkRange removes Attr from the span (start, end]. It records the
// add operations it cancels: every causally visible add of Attr that
// overlaps the span.
func (d *Doc) RemoveMarkRange(attr string, start, end Anchor) ID {
	id := d.nextID()
	pos := d.positions()
	n := len(d.Order())
	var cancels []ID
	for _, m := range d.adds {
		if m.Attr != attr {
			continue
		}
		if rangesOverlap(m.Start, m.End, start, end, pos, n) {
			cancels = append(cancels, m.ID)
		}
	}
	sort.Slice(cancels, func(i, j int) bool {
		if cancels[i].Actor != cancels[j].Actor {
			return cancels[i].Actor < cancels[j].Actor
		}
		return cancels[i].Seq < cancels[j].Seq
	})
	d.rems[id] = RemoveMark{ID: id, Attr: attr, Start: start, End: end, Cancels: cancels}
	return id
}

// Merge merges all operations of other into d. It is associative,
// commutative and idempotent.
func (d *Doc) Merge(other *Doc) {
	for id, c := range other.chars {
		if _, ok := d.chars[id]; !ok {
			d.chars[id] = c
			d.observe(id.Seq)
			d.dirty = true
		}
	}
	for id, m := range other.adds {
		if _, ok := d.adds[id]; !ok {
			d.adds[id] = m
			d.observe(m.Ts)
		}
	}
	for id, r := range other.rems {
		if _, ok := d.rems[id]; !ok {
			d.rems[id] = r
			d.observe(id.Seq)
		}
	}
}

// Order returns the character IDs in current document order.
func (d *Doc) Order() []ID {
	if d.dirty {
		d.order = d.computeOrder()
		d.dirty = false
	}
	return d.order
}

// Text returns the plain text of the document.
func (d *Doc) Text() string {
	var b strings.Builder
	for _, id := range d.Order() {
		b.WriteRune(d.chars[id].value)
	}
	return b.String()
}

// FormatAt returns the active attributes for the i-th character, or nil
// if the character has no formatting (or i is out of range).
func (d *Doc) FormatAt(i int) map[string]string {
	order := d.Order()
	if i < 0 || i >= len(order) {
		return nil
	}
	pos := d.positions()
	n := len(order)
	attrs := map[string]bool{}
	for _, m := range d.adds {
		attrs[m.Attr] = true
	}
	var out map[string]string
	for attr := range attrs {
		var best *AddMark
		for _, m := range d.adds {
			if m.Attr != attr || !spanCovers(m.Start, m.End, i, pos, n) {
				continue
			}
			if d.isCancelled(m, i, pos, n) {
				continue
			}
			if best == nil || markLater(m, *best) {
				mm := m
				best = &mm
			}
		}
		if best != nil {
			if out == nil {
				out = map[string]string{}
			}
			out[attr] = best.Value
		}
	}
	return out
}

// Span is a maximal run of characters sharing identical formatting.
type Span struct {
	Text   string
	Format map[string]string
}

// Spans returns the document as contiguous spans of uniform formatting.
func (d *Doc) Spans() []Span {
	order := d.Order()
	var spans []Span
	for i, id := range order {
		f := d.FormatAt(i)
		r := d.chars[id].value
		if n := len(spans); n > 0 && equalFormat(spans[n-1].Format, f) {
			spans[n-1].Text += string(r)
		} else {
			spans = append(spans, Span{Text: string(r), Format: f})
		}
	}
	return spans
}

func equalFormat(a, b map[string]string) bool {
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

// markLater reports whether add a wins over add b under the deterministic
// Lamport-timestamp-then-actor ordering.
func markLater(a, b AddMark) bool {
	if a.Ts != b.Ts {
		return a.Ts > b.Ts
	}
	return a.ID.Actor > b.ID.Actor
}

// isCancelled reports whether add m is cancelled at character index i by
// some remove that covers i and causally knew about m.
func (d *Doc) isCancelled(m AddMark, i int, pos map[ID]int, n int) bool {
	for _, r := range d.rems {
		if r.Attr != m.Attr {
			continue
		}
		if !spanCovers(r.Start, r.End, i, pos, n) {
			continue
		}
		for _, c := range r.Cancels {
			if c == m.ID {
				return true
			}
		}
	}
	return false
}

func (d *Doc) positions() map[ID]int {
	order := d.Order()
	pos := make(map[ID]int, len(order))
	for i, id := range order {
		pos[id] = i
	}
	return pos
}

// computeOrder flattens the character tree: children of one parent are
// ordered by comesBefore, subtrees are spliced in right after the parent.
func (d *Doc) computeOrder() []ID {
	children := make(map[ID][]ID)
	var roots []ID
	for id, c := range d.chars {
		if _, ok := d.chars[c.parent]; c.parent.IsZero() || !ok {
			roots = append(roots, id)
		} else {
			children[c.parent] = append(children[c.parent], id)
		}
	}
	less := func(ids []ID) func(i, j int) bool {
		return func(i, j int) bool { return ids[i].comesBefore(ids[j]) }
	}
	sort.Slice(roots, less(roots))
	for k := range children {
		sort.Slice(children[k], less(children[k]))
	}
	var order []ID
	var visit func(ids []ID)
	visit = func(ids []ID) {
		for _, id := range ids {
			order = append(order, id)
			visit(children[id])
		}
	}
	visit(roots)
	return order
}

// anchorIndex resolves an anchor to a character index. The doc-start
// sentinel resolves to -1 and the doc-end sentinel to n-1, so a span
// (start, end] covers indices start+1 .. end.
func anchorIndex(a Anchor, pos map[ID]int, n int) (int, bool) {
	if a.Char.IsZero() {
		if a.IsEnd {
			return n - 1, true
		}
		return -1, true
	}
	p, ok := pos[a.Char]
	return p, ok
}

// spanCovers reports whether the span (start, end] includes the character
// at index i.
func spanCovers(start, end Anchor, i int, pos map[ID]int, n int) bool {
	s, ok1 := anchorIndex(start, pos, n)
	e, ok2 := anchorIndex(end, pos, n)
	return ok1 && ok2 && s < i && i <= e
}

// rangesOverlap reports whether the spans (s1, e1] and (s2, e2] share at
// least one character.
func rangesOverlap(s1, e1, s2, e2 Anchor, pos map[ID]int, n int) bool {
	a1, ok1 := anchorIndex(s1, pos, n)
	b1, ok2 := anchorIndex(e1, pos, n)
	a2, ok3 := anchorIndex(s2, pos, n)
	b2, ok4 := anchorIndex(e2, pos, n)
	if !(ok1 && ok2 && ok3 && ok4) {
		return false
	}
	return a1+1 <= b2 && a2+1 <= b1
}
