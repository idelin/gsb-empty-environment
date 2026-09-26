// Package datrie implements a dynamic double-array trie (base/check).
//
// Encoding layout (all arrays are parallel, indexed by state id):
//
//	base  []int32   transition offset for each state
//	check []int32   parent state id; freeCheck (-1) marks a free slot
//	value []int64   payload attached to a terminal state
//	hasVal []bool   terminal flag, independent of children, so a word
//	                that is a prefix of another word is still a word
//
// Transition: from state s on byte c the next state is base[s]+c,
// valid iff check[base[s]+c] == s. There is no map anywhere: lookups
// are pure array indexing, and memory occupancy is exactly the four
// slices reported by Stats.
package datrie

const (
	freeCheck   int32 = -1
	rootIndex         = 0
	initialBase int32 = 1
	alphabet          = 256
)

// Trie is a dynamic double-array trie over byte strings with int64 values.
type Trie struct {
	base   []int32
	check  []int32
	value  []int64
	hasVal []bool
	n      int // number of stored keys
}

// New returns an empty trie.
func New() *Trie {
	t := &Trie{}
	t.grow(1)
	t.base[rootIndex] = initialBase
	t.check[rootIndex] = rootIndex // root is permanently occupied
	return t
}

// Len returns the number of stored keys.
func (t *Trie) Len() int { return t.n }

func (t *Trie) grow(n int) {
	for len(t.base) < n {
		t.base = append(t.base, 0)
		t.check = append(t.check, freeCheck)
		t.value = append(t.value, 0)
		t.hasVal = append(t.hasVal, false)
	}
}

// walk follows key from the root and returns the reached state.
func (t *Trie) walk(key string) (int32, bool) {
	s := int32(rootIndex)
	for i := 0; i < len(key); i++ {
		idx := int(t.base[s]) + int(key[i])
		if idx <= 0 || idx >= len(t.check) || t.check[idx] != s {
			return 0, false
		}
		s = int32(idx)
	}
	return s, true
}

// Get returns the value stored under key.
func (t *Trie) Get(key string) (int64, bool) {
	s, ok := t.walk(key)
	if !ok || !t.hasVal[s] {
		return 0, false
	}
	return t.value[s], true
}

// Put inserts key with value val. Re-inserting an existing key only
// updates the value in place: no new slot is allocated, so no orphan
// slots are produced.
func (t *Trie) Put(key string, val int64) {
	s := int32(rootIndex)
	for i := 0; i < len(key); i++ {
		s = t.insert(s, key[i])
	}
	if !t.hasVal[s] {
		t.n++
	}
	t.value[s] = val
	t.hasVal[s] = true
}

// insert returns the child state of s on byte c, creating it if needed.
func (t *Trie) insert(s int32, c byte) int32 {
	idx := int(t.base[s]) + int(c)
	if idx < len(t.check) {
		if t.check[idx] == s {
			return int32(idx) // transition already exists
		}
		if t.check[idx] == freeCheck {
			t.check[idx] = s
			return int32(idx)
		}
		// Slot occupied by another state: relocate the children of s.
		t.relocate(s, c)
		idx = int(t.base[s]) + int(c)
	}
	t.grow(idx + 1)
	t.check[idx] = s
	return int32(idx)
}

// childLabels lists the bytes on which s has children.
func (t *Trie) childLabels(s int32) []byte {
	labels := make([]byte, 0, 8)
	b := int(t.base[s])
	for c := 0; c < alphabet; c++ {
		idx := b + c
		if idx > 0 && idx < len(t.check) && t.check[idx] == s {
			labels = append(labels, byte(c))
		}
	}
	return labels
}

// findBase returns the smallest offset at which every label lands on a
// free slot.
func (t *Trie) findBase(labels []byte) int32 {
	b := int32(1)
outer:
	for {
		for _, l := range labels {
			idx := int(b) + int(l)
			if idx < len(t.check) && t.check[idx] != freeCheck {
				b++
				continue outer
			}
		}
		return b
	}
}

// relocate moves all children of s to a new base offset that also fits
// newLabel. Every moved child keeps its own subtree: the check values
// of the moved child's children are rewritten to point at the new slot.
func (t *Trie) relocate(s int32, newLabel byte) {
	oldBase := t.base[s]
	labels := t.childLabels(s)
	moved := len(labels)
	labels = append(labels, newLabel)
	newBase := t.findBase(labels)

	for _, l := range labels[:moved] {
		oldIdx := int(oldBase) + int(l)
		newIdx := int(newBase) + int(l)
		t.grow(newIdx + 1)

		t.base[newIdx] = t.base[oldIdx]
		t.value[newIdx] = t.value[oldIdx]
		t.hasVal[newIdx] = t.hasVal[oldIdx]
		t.check[newIdx] = s

		// Reparent the grandchildren: their check pointed at oldIdx.
		cb := int(t.base[newIdx])
		for c := 0; c < alphabet; c++ {
			ci := cb + c
			if ci > 0 && ci < len(t.check) && t.check[ci] == int32(oldIdx) {
				t.check[ci] = int32(newIdx)
			}
		}

		t.check[oldIdx] = freeCheck
		t.base[oldIdx] = 0
		t.value[oldIdx] = 0
		t.hasVal[oldIdx] = false
	}
	t.base[s] = newBase
}

// hasChildren reports whether s has at least one child.
func (t *Trie) hasChildren(s int32) bool {
	b := int(t.base[s])
	for c := 0; c < alphabet; c++ {
		idx := b + c
		if idx > 0 && idx < len(t.check) && t.check[idx] == s {
			return true
		}
	}
	return false
}

// Delete removes key and its value. Leaf states that become useless
// (no value, no children) are freed bottom-up so their slots can be
// reused by later insertions. Returns false if key was not present.
func (t *Trie) Delete(key string) bool {
	s, ok := t.walk(key)
	if !ok || !t.hasVal[s] {
		return false
	}
	t.hasVal[s] = false
	t.value[s] = 0
	t.n--
	for s != rootIndex && !t.hasVal[s] && !t.hasChildren(s) {
		parent := t.check[s]
		t.check[s] = freeCheck
		t.base[s] = 0
		s = parent
	}
	return true
}

// Entry is a key/value pair returned by Collect.
type Entry struct {
	Key   string
	Value int64
}

// Collect returns every key/value pair whose key starts with prefix,
// in lexicographic order. If prefix itself is a stored key it is
// included in the result.
func (t *Trie) Collect(prefix string) []Entry {
	s, ok := t.walk(prefix)
	if !ok {
		return nil
	}
	var out []Entry
	var dfs func(s int32, buf []byte)
	dfs = func(s int32, buf []byte) {
		if t.hasVal[s] {
			out = append(out, Entry{Key: string(buf), Value: t.value[s]})
		}
		b := int(t.base[s])
		for c := 0; c < alphabet; c++ {
			idx := b + c
			if idx > 0 && idx < len(t.check) && t.check[idx] == s {
				dfs(int32(idx), append(buf, byte(c)))
			}
		}
	}
	dfs(s, []byte(prefix))
	return out
}

// Stats reports the real memory layout of the trie.
type Stats struct {
	Keys       int // stored keys
	Slots      int // allocated array length (capacity in states)
	UsedSlots  int // occupied states
	BaseBytes  int // len(base)  * 4
	CheckBytes int // len(check) * 4
	ValueBytes int // len(value) * 8
	FlagBytes  int // len(hasVal)* 1
	TotalBytes int // sum of the four arrays
}

// Stats computes the current occupancy of the double arrays.
func (t *Trie) Stats() Stats {
	used := 0
	for _, c := range t.check {
		if c != freeCheck {
			used++
		}
	}
	st := Stats{
		Keys:       t.n,
		Slots:      len(t.base),
		UsedSlots:  used,
		BaseBytes:  4 * len(t.base),
		CheckBytes: 4 * len(t.check),
		ValueBytes: 8 * len(t.value),
		FlagBytes:  len(t.hasVal),
	}
	st.TotalBytes = st.BaseBytes + st.CheckBytes + st.ValueBytes + st.FlagBytes
	return st
}
