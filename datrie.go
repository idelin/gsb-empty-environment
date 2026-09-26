// Package datrie implements a dynamic double-array trie (Aoe 1989).
//
// Encoding layout (all arrays are parallel, indexed by slot id):
//
//	base[i]  int32  if slot i is used and has children: the base offset B such
//	                that the child along byte c lives at slot B+c.
//	                0 if the node currently has no children (base 0 is reserved,
//	                since slot 0 is the root and can never be a child slot).
//	                If slot i is free: link to the next free slot (free list).
//	check[i] int32  if slot i is used: the parent slot id, so every arc is
//	                verifiable via check[base[p]+c] == p. -1 marks a free slot.
//	                The root slot 0 carries the sentinel rootCheck.
//	value[i] int64  payload of the word ending at slot i (valid iff the
//	                terminal bit of i is set).
//	term[]   uint64 bitset: bit i set <=> a word ends at slot i. This is what
//	                lets a prefix of a word still be a word itself.
//
// Memory footprint: 4+4+8 bytes per slot plus 1/8 byte for the terminal bit,
// i.e. ~16.1 B/slot, independent of key length. Free slots are recycled
// through an intrusive free list threaded through base[].
package datrie

const (
	freeCheck int32 = -1
	rootCheck int32 = -2
	freeNil   int32 = -1
)

// Entry is a key/value pair returned by PrefixSearch.
type Entry struct {
	Key   string
	Value int64
}

// Trie is a dynamic double-array trie over byte strings.
type Trie struct {
	base     []int32
	check    []int32
	value    []int64
	term     []uint64
	freeHead int32 // head of the free list, linked via base[]; freeNil if empty
	used     int   // number of occupied slots (including root)
	words    int   // number of stored keys
}

// New returns an empty trie.
func New() *Trie {
	t := &Trie{freeHead: freeNil}
	t.grow(1)
	t.claim(0)
	t.check[0] = rootCheck
	t.base[0] = 0
	return t
}

// Len returns the number of stored keys.
func (t *Trie) Len() int { return t.words }

func (t *Trie) grow(n int) {
	for len(t.base) < n {
		i := len(t.base)
		t.base = append(t.base, t.freeHead)
		t.check = append(t.check, freeCheck)
		t.value = append(t.value, 0)
		if len(t.term) < i/64+1 {
			t.term = append(t.term, 0)
		}
		t.freeHead = int32(i)
	}
}

func (t *Trie) ensure(idx int) {
	if idx >= len(t.base) {
		t.grow(idx + 1)
	}
}

// claim removes idx from the free list and marks it used by the caller.
func (t *Trie) claim(idx int) {
	t.ensure(idx)
	prev, cur := freeNil, t.freeHead
	for cur != freeNil && int(cur) != idx {
		prev, cur = cur, t.base[cur]
	}
	if cur == freeNil {
		panic("datrie: claim of non-free slot")
	}
	if prev == freeNil {
		t.freeHead = t.base[idx]
	} else {
		t.base[prev] = t.base[idx]
	}
	t.base[idx] = 0
	t.value[idx] = 0
	t.used++
}

// release returns idx to the free list.
func (t *Trie) release(idx int) {
	t.check[idx] = freeCheck
	t.value[idx] = 0
	t.clearTerm(idx)
	t.base[idx] = t.freeHead
	t.freeHead = int32(idx)
	t.used--
}

func (t *Trie) isTerm(i int) bool { return t.term[i/64]>>(uint(i)%64)&1 == 1 }
func (t *Trie) setTerm(i int)     { t.term[i/64] |= 1 << (uint(i) % 64) }
func (t *Trie) clearTerm(i int)   { t.term[i/64] &^= 1 << (uint(i) % 64) }

// childrenOf returns the sorted outgoing byte labels of node s.
func (t *Trie) childrenOf(s int) []byte {
	if t.base[s] == 0 {
		return nil
	}
	var out []byte
	b := int(t.base[s])
	for c := 0; c < 256; c++ {
		idx := b + c
		if idx < len(t.check) && t.check[idx] == int32(s) {
			out = append(out, byte(c))
		}
	}
	return out
}

func (t *Trie) hasChildren(s int) bool {
	if t.base[s] == 0 {
		return false
	}
	b := int(t.base[s])
	for c := 0; c < 256; c++ {
		idx := b + c
		if idx < len(t.check) && t.check[idx] == int32(s) {
			return true
		}
	}
	return false
}

// findBase returns a base offset b >= 1 such that every slot b+label is free
// (or beyond the current arrays, in which case they are grown on claim).
// labels must be non-empty and sorted ascending.
func (t *Trie) findBase(labels []byte) int {
	minL := int(labels[0])
	for f := t.freeHead; f != freeNil; f = t.base[f] {
		b := int(f) - minL
		if b < 1 {
			continue
		}
		ok := true
		for _, l := range labels {
			s := b + int(l)
			if s < len(t.check) && t.check[s] != freeCheck {
				ok = false
				break
			}
		}
		if ok {
			return b
		}
	}
	// No reusable gap: place the block right past the current arrays.
	b := len(t.check) - minL
	if b < 1 {
		b = 1
	}
	return b
}

// relocate moves all children of node s to a fresh base offset that also has
// room for newLabel. Every moved child keeps its own base/value/terminal
// state, its check is rewritten to s, and every grandchild's check is
// rewritten from the old child slot to the new one, so no arc is orphaned.
func (t *Trie) relocate(s int, newLabel byte) {
	oldBase := int(t.base[s])
	labels := t.childrenOf(s)

	// all = labels ∪ {newLabel}, sorted.
	all := make([]byte, 0, len(labels)+1)
	inserted := false
	for _, c := range labels {
		if !inserted && newLabel < c {
			all = append(all, newLabel)
			inserted = true
		}
		all = append(all, c)
	}
	if !inserted {
		all = append(all, newLabel)
	}

	nb := t.findBase(all)
	for _, c := range labels {
		oldIdx := oldBase + int(c)
		newIdx := nb + int(c)
		t.claim(newIdx)
		t.base[newIdx] = t.base[oldIdx]
		t.value[newIdx] = t.value[oldIdx]
		if t.isTerm(oldIdx) {
			t.setTerm(newIdx)
		}
		t.check[newIdx] = int32(s)
		// Repoint grandchildren: their check must name the new child slot.
		if ob := int(t.base[oldIdx]); ob != 0 {
			for gc := 0; gc < 256; gc++ {
				g := ob + gc
				if g < len(t.check) && t.check[g] == int32(oldIdx) {
					t.check[g] = int32(newIdx)
				}
			}
		}
		t.release(oldIdx)
	}
	t.base[s] = int32(nb)
}

// Insert stores key with the given value. Re-inserting an existing key only
// updates its value; no new slots are allocated and no orphans are created.
func (t *Trie) Insert(key string, value int64) {
	s := 0
	for i := 0; i < len(key); i++ {
		c := key[i]
		if t.base[s] == 0 {
			t.base[s] = int32(t.findBase([]byte{c}))
		}
		idx := int(t.base[s]) + int(c)
		switch {
		case idx >= len(t.check) || t.check[idx] == freeCheck:
			t.claim(idx)
			t.check[idx] = int32(s)
		case t.check[idx] != int32(s):
			// Slot occupied by another parent: relocate s's children.
			t.relocate(s, c)
			idx = int(t.base[s]) + int(c)
			t.claim(idx)
			t.check[idx] = int32(s)
		}
		s = idx
	}
	if !t.isTerm(s) {
		t.setTerm(s)
		t.words++
	}
	t.value[s] = value
}

// walk follows key from the root and returns the reached slot.
func (t *Trie) walk(key string) (int, bool) {
	s := 0
	for i := 0; i < len(key); i++ {
		if t.base[s] == 0 {
			return 0, false
		}
		idx := int(t.base[s]) + int(key[i])
		if idx >= len(t.check) || t.check[idx] != int32(s) {
			return 0, false
		}
		s = idx
	}
	return s, true
}

// Search returns the value stored for key.
func (t *Trie) Search(key string) (int64, bool) {
	s, ok := t.walk(key)
	if !ok || !t.isTerm(s) {
		return 0, false
	}
	return t.value[s], true
}

// Delete removes key. Leaf slots that become childless and non-terminal are
// pruned and recycled bottom-up, so the trie shrinks after deletions.
// It reports whether the key existed.
func (t *Trie) Delete(key string) bool {
	path := []int{0}
	s := 0
	for i := 0; i < len(key); i++ {
		if t.base[s] == 0 {
			return false
		}
		idx := int(t.base[s]) + int(key[i])
		if idx >= len(t.check) || t.check[idx] != int32(s) {
			return false
		}
		s = idx
		path = append(path, s)
	}
	if !t.isTerm(s) {
		return false
	}
	t.clearTerm(s)
	t.value[s] = 0
	t.words--
	for i := len(path) - 1; i >= 1; i-- {
		n := path[i]
		if t.isTerm(n) || t.hasChildren(n) {
			break
		}
		t.release(n)
	}
	return true
}

// PrefixSearch returns all entries whose key starts with prefix, in
// lexicographic (byte) order. The prefix itself is included if it is a word.
func (t *Trie) PrefixSearch(prefix string) []Entry {
	s, ok := t.walk(prefix)
	if !ok {
		return nil
	}
	var out []Entry
	var collect func(n int, key string)
	collect = func(n int, key string) {
		if t.isTerm(n) {
			out = append(out, Entry{Key: key, Value: t.value[n]})
		}
		if t.base[n] == 0 {
			return
		}
		b := int(t.base[n])
		for c := 0; c < 256; c++ {
			idx := b + c
			if idx < len(t.check) && t.check[idx] == int32(n) {
				collect(idx, key+string([]byte{byte(c)}))
			}
		}
	}
	collect(s, prefix)
	return out
}

// Stats reports the real memory layout of the trie.
type Stats struct {
	Slots      int // total allocated slots
	Used       int // occupied slots (including root)
	Free       int // free slots, recycled via the free list
	Words      int // stored keys
	BaseBytes  int // len(base) * 4
	CheckBytes int // len(check) * 4
	ValueBytes int // len(value) * 8
	TermBytes  int // len(term) * 8
	TotalBytes int // sum of the above
}

// Stats returns the current memory footprint of the double arrays.
func (t *Trie) Stats() Stats {
	st := Stats{
		Slots:      len(t.base),
		Used:       t.used,
		Free:       len(t.base) - t.used,
		Words:      t.words,
		BaseBytes:  len(t.base) * 4,
		CheckBytes: len(t.check) * 4,
		ValueBytes: len(t.value) * 8,
		TermBytes:  len(t.term) * 8,
	}
	st.TotalBytes = st.BaseBytes + st.CheckBytes + st.ValueBytes + st.TermBytes
	return st
}
