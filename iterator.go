package pathtree

import "unsafe"

// Iterator performs ordered, snapshot-stable iteration over an inclusive key
// range. A zero-value iterator is exhausted.
type Iterator struct {
	root    *node
	stack   []*node
	cur     *node
	low     string
	high    string
	hasLow  bool
	hasHigh bool
}

// IteratorSize returns unsafe.Sizeof(Iterator{}), excluding its dynamic stack.
func IteratorSize() uintptr {
	return unsafe.Sizeof(Iterator{})
}

// Iterator returns an iterator over all keys in ascending order.
func (t *Tree) Iterator() *Iterator {
	return t.Range("", "", false, false)
}

// RangeFrom returns an iterator over all keys greater than or equal to low.
func (t *Tree) RangeFrom(low string) *Iterator {
	return t.Range(low, "", true, false)
}

// RangeUntil returns an iterator over all keys less than or equal to high.
func (t *Tree) RangeUntil(high string) *Iterator {
	return t.Range("", high, false, true)
}

// Range returns an inclusive iterator. Bounds are enabled through hasLow and
// hasHigh. If hasLow and hasHigh are both true and low is greater than high,
// the iterator starts exhausted.
func (t *Tree) Range(low, high string, hasLow, hasHigh bool) *Iterator {
	it := &Iterator{
		root:    t.root,
		low:     low,
		high:    high,
		hasLow:  hasLow,
		hasHigh: hasHigh,
	}
	if hasLow && hasHigh && low > high {
		return it
	}
	it.pushLeftSpine(t.root)
	it.advance()
	return it
}

// Next advances to the next in-range key/value pair.
func (it *Iterator) Next() bool {
	if it == nil || it.cur == nil {
		return false
	}
	it.advance()
	return it.cur != nil
}

// Key returns the current key.
func (it *Iterator) Key() string {
	if it == nil || it.cur == nil {
		return ""
	}
	return it.cur.key
}

// Value returns a private copy of the current value.
func (it *Iterator) Value() []byte {
	if it == nil || it.cur == nil {
		return nil
	}
	return cloneBytes(it.cur.value)
}

// KeyValue returns the current key and a private copy of its value.
func (it *Iterator) KeyValue() (string, []byte) {
	return it.Key(), it.Value()
}

func (it *Iterator) pushLeftSpine(n *node) {
	for n != nil {
		if it.hasLow && n.key < it.low {
			n = n.right
			continue
		}
		it.stack = append(it.stack, n)
		n = n.left
	}
}

func (it *Iterator) advance() {
	it.cur = nil
	for len(it.stack) > 0 {
		last := len(it.stack) - 1
		n := it.stack[last]
		it.stack = it.stack[:last]

		if it.hasLow && n.key < it.low {
			it.pushLeftSpine(n.right)
			continue
		}
		if it.hasHigh && n.key > it.high {
			continue
		}

		it.pushLeftSpine(n.right)
		it.cur = n
		return
	}
}
