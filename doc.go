package undocrdt

import (
	"fmt"
	"sort"
	"strings"
)

// char is one node of the sequence CRDT.
type char struct {
	id      OpID
	after   OpID // left-origin
	value   rune
	visible bool // tombstone semantics: deleted chars stay in the graph
}

// Doc is a single replica of the collaborative document. Replicas converge
// by exchanging Ops (in any order); Undo/Redo state is local to each site.
type Doc struct {
	site string
	seq  int

	chars map[OpID]*char
	ops   map[OpID]*Op // every node of the undo tree
	edges [][2]OpID    // compensation -> reversed node

	pending []Op // ops waiting for missing causal dependencies

	undoStack []OpID        // this site's user ops, oldest first
	redoStack []OpID        // ops undone by this site, most recent last
	compOf    map[OpID]OpID // user op -> its latest compensation
}

// NewDoc creates a replica owned by site. Site names must be unique across
// collaborators.
func NewDoc(site string) *Doc {
	return &Doc{
		site:   site,
		chars:  make(map[OpID]*char),
		ops:    make(map[OpID]*Op),
		compOf: make(map[OpID]OpID),
	}
}

// Site returns the replica's site name.
func (d *Doc) Site() string { return d.site }

// LocalInsert inserts text after the character with ID after (use RootID
// for the start of the document) and returns the op to broadcast to the
// other replicas.
func (d *Doc) LocalInsert(after OpID, text string) Op {
	runes := []rune(text)
	if len(runes) == 0 {
		panic("undocrdt: cannot insert empty text")
	}
	op := Op{
		ID:    OpID{Site: d.site, Seq: d.seq + 1},
		Kind:  Insert,
		After: after,
		Text:  text,
	}
	d.seq += len(runes)
	d.applyLocal(op)
	return op
}

// LocalDelete removes the given characters (a user edit) and returns the op
// to broadcast.
func (d *Doc) LocalDelete(targets []OpID) Op {
	if len(targets) == 0 {
		panic("undocrdt: delete needs at least one target")
	}
	d.seq++
	op := Op{
		ID:      OpID{Site: d.site, Seq: d.seq},
		Kind:    Delete,
		Targets: append([]OpID(nil), targets...),
	}
	d.applyLocal(op)
	return op
}

// Undo reverses this site's most recent user edit by appending a
// compensation node to the undo tree. Other collaborators' concurrent
// edits are untouched. It returns the compensation op to broadcast, or
// false when there is nothing to undo.
func (d *Doc) Undo() (Op, bool) {
	if len(d.undoStack) == 0 {
		return Op{}, false
	}
	origID := d.undoStack[len(d.undoStack)-1]
	d.undoStack = d.undoStack[:len(d.undoStack)-1]
	orig := d.ops[origID]

	var comp Op
	switch orig.Kind {
	case Insert:
		comp = d.newComp(Delete, orig.CharIDs())
	case Delete:
		comp = d.newComp(Restore, orig.Targets)
	default:
		panic("undocrdt: cannot undo compensation op")
	}
	comp.Undoes = orig.ID
	d.applyLocal(comp)

	d.compOf[orig.ID] = comp.ID
	d.redoStack = append(d.redoStack, orig.ID)
	return comp, true
}

// Redo re-applies this site's most recently undone edit by appending a
// compensation node that reverses the undo. It returns the op to
// broadcast, or false when there is nothing to redo.
func (d *Doc) Redo() (Op, bool) {
	if len(d.redoStack) == 0 {
		return Op{}, false
	}
	origID := d.redoStack[len(d.redoStack)-1]
	d.redoStack = d.redoStack[:len(d.redoStack)-1]
	orig := d.ops[origID]

	var comp Op
	switch orig.Kind {
	case Insert:
		comp = d.newComp(Restore, orig.CharIDs())
	case Delete:
		comp = d.newComp(Delete, orig.Targets)
	default:
		panic("undocrdt: cannot redo compensation op")
	}
	// The edge points at the undo compensation being reversed, keeping the
	// undo tree a proper chain: orig <- undo-comp <- redo-comp.
	comp.Undoes = d.compOf[orig.ID]
	d.applyLocal(comp)

	d.undoStack = append(d.undoStack, orig.ID)
	return comp, true
}

func (d *Doc) newComp(kind Kind, targets []OpID) Op {
	d.seq++
	return Op{
		ID:      OpID{Site: d.site, Seq: d.seq},
		Kind:    kind,
		Targets: append([]OpID(nil), targets...),
	}
}

// applyLocal applies a locally generated op and records it on the undo
// stack when it is a user edit.
func (d *Doc) applyLocal(op Op) {
	d.apply(op)
	if !op.IsCompensation() {
		d.undoStack = append(d.undoStack, op.ID)
		d.redoStack = d.redoStack[:0] // a fresh edit invalidates redo history
	}
}

// Apply integrates a remote op received from another replica. Delivery may
// be out of order: ops with missing causal dependencies are buffered and
// applied once their dependencies arrive. Apply is idempotent.
func (d *Doc) Apply(op Op) { d.apply(op) }

func (d *Doc) apply(op Op) {
	if _, ok := d.ops[op.ID]; ok {
		return // idempotent re-delivery
	}
	if !d.ready(op) {
		d.pending = append(d.pending, op)
		return
	}
	d.integrate(op)
	d.drainPending()
}

func (d *Doc) ready(op Op) bool {
	switch op.Kind {
	case Insert:
		return op.After.IsRoot() || d.chars[op.After] != nil
	default: // Delete / Restore
		for _, t := range op.Targets {
			if d.chars[t] == nil {
				return false
			}
		}
		return true
	}
}

func (d *Doc) integrate(op Op) {
	opCopy := op
	d.ops[op.ID] = &opCopy
	switch op.Kind {
	case Insert:
		prev := op.After
		for i, r := range []rune(op.Text) {
			id := OpID{Site: op.ID.Site, Seq: op.ID.Seq + i}
			d.chars[id] = &char{id: id, after: prev, value: r, visible: true}
			prev = id
		}
	case Delete:
		for _, t := range op.Targets {
			d.chars[t].visible = false
		}
	case Restore:
		for _, t := range op.Targets {
			d.chars[t].visible = true
		}
	}
	if op.IsCompensation() {
		d.edges = append(d.edges, [2]OpID{op.ID, op.Undoes})
	}
}

func (d *Doc) drainPending() {
	for len(d.pending) > 0 {
		progress := false
		rest := d.pending[:0]
		for _, op := range d.pending {
			if _, ok := d.ops[op.ID]; ok {
				progress = true
				continue
			}
			if d.ready(op) {
				d.integrate(op)
				progress = true
			} else {
				rest = append(rest, op)
			}
		}
		d.pending = rest
		if !progress {
			return
		}
	}
}

// String materializes the visible document content. Concurrent inserts at
// the same position are ordered deterministically (higher Seq first, then
// site name), so every replica renders the same string.
func (d *Doc) String() string {
	children := make(map[OpID][]*char)
	for _, c := range d.chars {
		children[c.after] = append(children[c.after], c)
	}
	for _, sibs := range children {
		sort.Slice(sibs, func(i, j int) bool {
			if sibs[i].id.Seq != sibs[j].id.Seq {
				return sibs[i].id.Seq > sibs[j].id.Seq
			}
			return sibs[i].id.Site > sibs[j].id.Site
		})
	}

	var b strings.Builder
	var walk func(parent OpID)
	walk = func(parent OpID) {
		for _, c := range children[parent] {
			if c.visible {
				b.WriteRune(c.value)
			}
			walk(c.id) // children of tombstones stay visible
		}
	}
	walk(RootID)
	return b.String()
}

// LastVisible returns the ID of the last visible character, or RootID when
// the document is empty. It is the natural left-origin for appending.
func (d *Doc) LastVisible() OpID {
	last := RootID
	found := false
	children := make(map[OpID][]*char)
	for _, c := range d.chars {
		children[c.after] = append(children[c.after], c)
	}
	for _, sibs := range children {
		sort.Slice(sibs, func(i, j int) bool {
			if sibs[i].id.Seq != sibs[j].id.Seq {
				return sibs[i].id.Seq > sibs[j].id.Seq
			}
			return sibs[i].id.Site > sibs[j].id.Site
		})
	}
	var walk func(parent OpID)
	walk = func(parent OpID) {
		for _, c := range children[parent] {
			if c.visible {
				last, found = c.id, true
			}
			walk(c.id)
		}
	}
	walk(RootID)
	if !found {
		return RootID
	}
	return last
}

// UndoTree renders the undo tree (nodes and compensation edges) for
// inspection and debugging.
func (d *Doc) UndoTree() string {
	ids := make([]OpID, 0, len(d.ops))
	for id := range d.ops {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool {
		if ids[i].Site != ids[j].Site {
			return ids[i].Site < ids[j].Site
		}
		return ids[i].Seq < ids[j].Seq
	})
	var b strings.Builder
	for _, id := range ids {
		op := d.ops[id]
		fmt.Fprintf(&b, "node %s#%d %-7s", op.ID.Site, op.ID.Seq, op.Kind)
		if op.Kind == Insert {
			fmt.Fprintf(&b, " %q", op.Text)
		}
		if op.IsCompensation() {
			fmt.Fprintf(&b, "  undoes-> %s#%d", op.Undoes.Site, op.Undoes.Seq)
		}
		b.WriteByte('\n')
	}
	return b.String()
}
