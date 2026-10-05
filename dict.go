// Package redact implements streaming log redaction based on a versioned
// dictionary of sensitive words.
//
// Matching semantics:
//   - Longest match wins at each input position.
//   - Ties (same word registered under multiple entry IDs) are decided by
//     the smallest entry ID.
//   - Matches never overlap: after a match is emitted, scanning resumes
//     right after it.
//   - Streaming: output is committed only for prefixes that can no longer
//     be extended by future input. A trailing fragment that is a proper
//     prefix of some dictionary entry stays pending.
//   - Dictionary versions take effect at explicit byte boundaries: bytes
//     written before SetVersion keep the dictionary they were written
//     under, even while sitting in the pending buffer. A match is governed
//     by the dictionary in effect at the position where it starts.
package redact

import "sort"

// Entry is a single dictionary word and its replacement.
type Entry struct {
	ID          int
	Word        string
	Replacement string
}

// terminal marks the end of a word in the trie.
type terminal struct {
	id          int
	replacement string
}

type node struct {
	children map[byte]*node
	term     *terminal
}

// Dictionary is an immutable snapshot of entries. Build one directly with
// NewDictionary, or derive versioned snapshots from a VersionedDict.
type Dictionary struct {
	root   *node
	maxLen int // length of the longest word; bounds the per-position walk
}

// NewDictionary builds a dictionary snapshot from entries. Empty words are
// ignored. If the same word appears under several IDs, the smallest ID wins.
func NewDictionary(entries ...Entry) *Dictionary {
	d := &Dictionary{root: &node{}}
	for _, e := range entries {
		if e.Word == "" {
			continue
		}
		cur := d.root
		for i := 0; i < len(e.Word); i++ {
			c := e.Word[i]
			if cur.children == nil {
				cur.children = make(map[byte]*node)
			}
			next := cur.children[c]
			if next == nil {
				next = &node{}
				cur.children[c] = next
			}
			cur = next
		}
		if cur.term == nil || e.ID < cur.term.id {
			cur.term = &terminal{id: e.ID, replacement: e.Replacement}
		}
		if len(e.Word) > d.maxLen {
			d.maxLen = len(e.Word)
		}
	}
	return d
}

type dictOp struct {
	version int
	seq     int
	remove  bool
	entry   Entry
}

// VersionedDict records entry additions and removals tagged with the
// version at which they take effect, and materializes immutable Dictionary
// snapshots via At.
type VersionedDict struct {
	ops []dictOp
	seq int
}

// Add records that entry e exists from the given version onward.
func (v *VersionedDict) Add(version int, e Entry) {
	v.ops = append(v.ops, dictOp{version: version, seq: v.seq, entry: e})
	v.seq++
}

// Remove records that the entry with the given ID is absent from the given
// version onward.
func (v *VersionedDict) Remove(version int, id int) {
	v.ops = append(v.ops, dictOp{version: version, seq: v.seq, remove: true, entry: Entry{ID: id}})
	v.seq++
}

// At returns the dictionary snapshot in effect for the given version: all
// operations with version <= the requested one, applied in version order
// (ties broken by call order).
func (v *VersionedDict) At(version int) *Dictionary {
	ops := make([]dictOp, 0, len(v.ops))
	for _, op := range v.ops {
		if op.version <= version {
			ops = append(ops, op)
		}
	}
	sort.SliceStable(ops, func(i, j int) bool {
		if ops[i].version != ops[j].version {
			return ops[i].version < ops[j].version
		}
		return ops[i].seq < ops[j].seq
	})
	alive := make(map[int]Entry)
	for _, op := range ops {
		if op.remove {
			delete(alive, op.entry.ID)
		} else {
			alive[op.entry.ID] = op.entry
		}
	}
	ids := make([]int, 0, len(alive))
	for id := range alive {
		ids = append(ids, id)
	}
	sort.Ints(ids)
	entries := make([]Entry, 0, len(ids))
	for _, id := range ids {
		entries = append(entries, alive[id])
	}
	return NewDictionary(entries...)
}
