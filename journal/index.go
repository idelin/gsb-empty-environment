package journal

import "sort"

// renameHalf is an unpaired side of a rename. Rename-old and rename-new
// records arrive independently and may arrive in either order; both sides are
// held here until the matching half shows up. The half must survive
// checkpoints so a restart can still complete the rename.
type renameHalf struct {
	Record Record
}

// Index is the mutable directory index. Every mutation goes through
// applyRecord, which is the single transaction boundary: a rename is only
// materialized once both halves are present, so the index never contains a
// half-applied rename.
type Index struct {
	// Objects is keyed by full (ref, seq) identity.
	Objects map[FileID]*Object `json:"objects"`
	// Entries maps each directory entry to the object it names.
	Entries map[LinkKey]FileID `json:"entries"`
	// Graveyard remembers deleted identities so replayed records belonging to
	// a dead object (or an old sequence whose ref was since reused) are ignored.
	Graveyard map[FileID]uint64 `json:"graveyard"`
	// PendingOld / PendingNew hold unpaired rename halves, keyed by object id.
	PendingOld map[FileID]renameHalf `json:"pending_old"`
	PendingNew map[FileID]renameHalf `json:"pending_new"`

	Stats Stats `json:"stats"`
}

// Object is one file object and its current hard-link set.
type Object struct {
	ID        FileID    `json:"id"`
	FirstSeen int64     `json:"first_seen"`
	Links     []LinkKey `json:"links"`
}

func newIndex() *Index {
	return &Index{
		Objects:    map[FileID]*Object{},
		Entries:    map[LinkKey]FileID{},
		Graveyard:  map[FileID]uint64{},
		PendingOld: map[FileID]renameHalf{},
		PendingNew: map[FileID]renameHalf{},
	}
}

// applyOutcome tells the processor how the index treated a record.
type applyOutcome int

const (
	outcomeApplied  applyOutcome = iota
	outcomeStale                 // record belonged to a dead/superseded identity
	outcomeConflict              // record contradicted the current index state
)

// applyRecord mutates the index under one logical transaction.
func (ix *Index) applyRecord(r Record) applyOutcome {
	if _, dead := ix.Graveyard[r.ID]; dead {
		ix.Stats.StaleSkipped++
		return outcomeStale
	}

	switch r.Reason {
	case ReasonCreate:
		return ix.applyCreate(r)
	case ReasonLinkAdd:
		return ix.applyLinkAdd(r)
	case ReasonRenameOld:
		ix.PendingOld[r.ID] = renameHalf{Record: r}
		return ix.tryPairRename(r.ID)
	case ReasonRenameNew:
		ix.PendingNew[r.ID] = renameHalf{Record: r}
		return ix.tryPairRename(r.ID)
	case ReasonLinkRemove:
		return ix.applyLinkRemove(r)
	case ReasonDelete:
		return ix.applyDelete(r)
	default:
		return outcomeConflict
	}
}

// alive returns the object only when its full (ref, seq) identity matches.
// A ref number reused under a different sequence fails this lookup, which is
// what keeps old records from being replayed onto the new occupant.
func (ix *Index) alive(id FileID) *Object {
	obj, ok := ix.Objects[id]
	if !ok {
		return nil
	}
	return obj
}

func (ix *Index) applyCreate(r Record) applyOutcome {
	if obj := ix.alive(r.ID); obj != nil {
		// Idempotent replay of a committed record.
		if hasLink(obj, r.oldKey()) {
			return outcomeApplied
		}
	}
	if occupant, taken := ix.Entries[r.oldKey()]; taken {
		// The entry name exists for a different identity: the create cannot be
		// valid against this index state.
		if occupant != r.ID {
			ix.Stats.Conflicts++
			return outcomeConflict
		}
	}
	obj := ix.Objects[r.ID]
	if obj == nil {
		obj = &Object{ID: r.ID, FirstSeen: r.TimeMicros}
		ix.Objects[r.ID] = obj
		ix.Stats.Creates++
	}
	ix.addLink(obj, r.oldKey())
	return outcomeApplied
}

func (ix *Index) applyLinkAdd(r Record) applyOutcome {
	obj := ix.alive(r.ID)
	if obj == nil {
		// Either an already-deleted object (handled by graveyard) or a record
		// whose create was never observed. Either way there is no live object
		// to attach the link to, so the record cannot be applied.
		ix.Stats.StaleSkipped++
		return outcomeStale
	}
	key := r.newKey()
	if occupant, taken := ix.Entries[key]; taken && occupant != r.ID {
		ix.Stats.Conflicts++
		return outcomeConflict
	}
	if hasLink(obj, key) {
		return outcomeApplied
	}
	ix.addLink(obj, key)
	ix.Stats.LinksAdded++
	return outcomeApplied
}

func (ix *Index) applyLinkRemove(r Record) applyOutcome {
	obj := ix.alive(r.ID)
	if obj == nil {
		// Dead identity (graveyard) or an object we never observed created.
		ix.Stats.StaleSkipped++
		return outcomeStale
	}
	key := r.oldKey()
	if occupant, taken := ix.Entries[key]; taken && occupant != r.ID {
		ix.Stats.Conflicts++
		return outcomeConflict
	}
	if !hasLink(obj, key) {
		return outcomeApplied
	}
	ix.removeLink(obj, key)
	delete(ix.Entries, key)
	ix.Stats.LinksRemoved++
	return outcomeApplied
}

func (ix *Index) applyDelete(r Record) applyOutcome {
	obj := ix.alive(r.ID)
	if obj == nil {
		if _, known := ix.Objects[r.ID]; !known {
			ix.Stats.StaleSkipped++
			return outcomeStale
		}
	}
	if obj != nil {
		for _, key := range obj.Links {
			if owner, ok := ix.Entries[key]; ok && owner == r.ID {
				delete(ix.Entries, key)
			}
		}
	}
	delete(ix.Objects, r.ID)
	delete(ix.PendingOld, r.ID)
	delete(ix.PendingNew, r.ID)
	ix.Graveyard[r.ID] = r.USN
	ix.Stats.Deletes++
	return outcomeApplied
}

// tryPairRename attempts to complete a rename for which a new half arrived (or
// whose old half just arrived). The directory entry is moved as a single
// mutation, so any snapshot taken before or after sees either the old entry or
// the new one, never both and never neither for a completed rename.
func (ix *Index) tryPairRename(id FileID) applyOutcome {
	oldHalf, hasOld := ix.PendingOld[id]
	newHalf, hasNew := ix.PendingNew[id]
	if !hasOld || !hasNew {
		return outcomeApplied
	}

	obj := ix.alive(id)
	if obj == nil {
		// Object vanished while the rename was in flight. Discard both halves;
		// any further records for this identity are graveyard-stale.
		delete(ix.PendingOld, id)
		delete(ix.PendingNew, id)
		ix.Stats.StaleSkipped++
		return outcomeStale
	}

	from := oldHalf.Record.oldKey()
	to := newHalf.Record.newKey()

	if !hasLink(obj, from) {
		// Old side names an entry we do not own: cannot pair safely.
		ix.Stats.Conflicts++
		return outcomeConflict
	}
	if occupant, taken := ix.Entries[to]; taken && occupant != id {
		// Destination belongs to another object. A real rename would have
		// displaced it via its own records; without those, this is a conflict.
		ix.Stats.Conflicts++
		return outcomeConflict
	}

	ix.removeLink(obj, from)
	delete(ix.Entries, from)
	ix.addLink(obj, to)
	ix.Entries[to] = id

	delete(ix.PendingOld, id)
	delete(ix.PendingNew, id)
	ix.Stats.Renames++
	return outcomeApplied
}

func (ix *Index) addLink(obj *Object, key LinkKey) {
	if hasLink(obj, key) {
		return
	}
	obj.Links = append(obj.Links, key)
	sort.Slice(obj.Links, func(i, j int) bool {
		if obj.Links[i].ParentRef != obj.Links[j].ParentRef {
			return obj.Links[i].ParentRef < obj.Links[j].ParentRef
		}
		return obj.Links[i].Name < obj.Links[j].Name
	})
	ix.Entries[key] = obj.ID
}

func (ix *Index) removeLink(obj *Object, key LinkKey) {
	kept := obj.Links[:0]
	for _, l := range obj.Links {
		if l != key {
			kept = append(kept, l)
		}
	}
	obj.Links = kept
}

func hasLink(obj *Object, key LinkKey) bool {
	for _, l := range obj.Links {
		if l == key {
			return true
		}
	}
	return false
}

// pendingCount exposes unpaired halves for tests/diagnostics.
func (ix *Index) pendingCount() int {
	return len(ix.PendingOld) + len(ix.PendingNew)
}
