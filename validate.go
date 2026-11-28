package blink

import (
	"bytes"
	"fmt"
)

// Validate checks the tree's structural invariants. It is intended for tests
// and diagnostics and must be called while the tree is quiescent.
func (t *Tree) Validate() error {
	root := t.root.Load()
	if root == nil {
		return fmt.Errorf("blink: nil root")
	}

	seen := make(map[*node]bool)
	treeLeaves := make(map[*node]bool)
	if err := t.validateInternal(root, seen, treeLeaves); err != nil {
		return err
	}

	type leafSnapshot struct {
		id    uint64
		high  []byte
		keys  [][]byte
		right *node
		dead  bool
	}

	var (
		chainLeaves = make(map[*node]bool)
		snapshots   []leafSnapshot
		leaf        = t.descendLeftmost()
	)
	for leaf != nil {
		if chainLeaves[leaf] {
			return fmt.Errorf("blink: cycle in leaf right links at page %d", leaf.id)
		}
		chainLeaves[leaf] = true

		leaf.mu.RLock()
		snapshot := leafSnapshot{
			id:    leaf.id,
			high:  cloneBound(leaf.highKey),
			right: leaf.rightLink,
			dead:  leaf.dead,
		}
		if !leaf.leaf {
			leaf.mu.RUnlock()
			return fmt.Errorf("blink: non-leaf page %d in leaf chain", leaf.id)
		}
		if !leaf.dead {
			snapshot.keys = append([][]byte(nil), leaf.keys...)
			if len(leaf.keys) != len(leaf.values) {
				leaf.mu.RUnlock()
				return fmt.Errorf("blink: page %d key/value length mismatch", leaf.id)
			}
		}
		leaf.mu.RUnlock()

		if !snapshot.dead {
			snapshots = append(snapshots, snapshot)
		}
		leaf = snapshot.right
	}

	var (
		prevKey []byte
		haveKey bool
		count   int64
	)
	for i, live := range snapshots {
		for j, key := range live.keys {
			if j > 0 && bytes.Compare(live.keys[j-1], key) >= 0 {
				return fmt.Errorf("blink: page %d keys are not strictly sorted", live.id)
			}
			if haveKey && bytes.Compare(prevKey, key) >= 0 {
				return fmt.Errorf("blink: leaf chain order violation at page %d: prev=%q key=%q", live.id, prevKey, key)
			}
			if live.high != nil && bytes.Compare(key, live.high) > 0 {
				return fmt.Errorf("blink: page %d key %q exceeds its high key %q", live.id, key, live.high)
			}
			prevKey = key
			haveKey = true
			count++
		}

		if i+1 < len(snapshots) {
			if live.high == nil {
				return fmt.Errorf("blink: unbounded live leaf %d has a right sibling", live.id)
			}
			next := snapshots[i+1]
			if len(next.keys) > 0 && bytes.Compare(next.keys[0], live.high) <= 0 {
				return fmt.Errorf("blink: page %d starts at %q, not above predecessor high key %q",
					next.id, next.keys[0], live.high)
			}
		} else if live.high != nil {
			return fmt.Errorf("blink: rightmost live leaf %d is bounded by %q", live.id, live.high)
		}
	}

	if count != t.count.Load() {
		return fmt.Errorf("blink: leaf chain has %d keys, Len reports %d", count, t.count.Load())
	}
	for page := range treeLeaves {
		if !chainLeaves[page] {
			return fmt.Errorf("blink: reachable leaf page %d is absent from the leaf chain", page.id)
		}
	}
	for page := range chainLeaves {
		if page.deadPage() {
			continue
		}
		if !treeLeaves[page] {
			return fmt.Errorf("blink: live leaf page %d is not reachable from the root", page.id)
		}
	}

	for _, live := range snapshots {
		for _, key := range live.keys {
			if _, ok := t.Get(key); !ok {
				return fmt.Errorf("blink: lookup missed key %q", key)
			}
		}
	}
	return nil
}

// Check is an alias for Validate.
func (t *Tree) Check() error {
	return t.Validate()
}

func (t *Tree) validateInternal(page *node, seen, leaves map[*node]bool) error {
	if page == nil {
		return fmt.Errorf("blink: nil child")
	}
	if seen[page] {
		return fmt.Errorf("blink: page %d is reachable more than once", page.id)
	}
	seen[page] = true

	page.mu.RLock()
	if page.dead {
		page.mu.RUnlock()
		return fmt.Errorf("blink: dead page %d reachable from root", page.id)
	}
	if page.leaf {
		page.mu.RUnlock()
		if leaves[page] {
			return fmt.Errorf("blink: leaf page %d is reachable more than once", page.id)
		}
		leaves[page] = true
		return nil
	}

	high := cloneBound(page.highKey)
	children := append([]*node(nil), page.children...)
	bounds := append([][]byte(nil), page.bounds...)
	page.mu.RUnlock()

	if len(children) == 0 {
		return fmt.Errorf("blink: internal page %d has no children", page.id)
	}
	if len(children) != len(bounds) {
		return fmt.Errorf("blink: internal page %d child/bound length mismatch", page.id)
	}
	for i, bound := range bounds {
		if i > 0 {
			prev := bounds[i-1]
			if prev == nil {
				return fmt.Errorf("blink: internal page %d has a bound after +inf", page.id)
			}
			if bound != nil && bytes.Compare(prev, bound) > 0 {
				return fmt.Errorf("blink: internal page %d bounds are not sorted", page.id)
			}
		}
		if high != nil && bound != nil && bytes.Compare(bound, high) > 0 {
			return fmt.Errorf("blink: internal page %d bound %q exceeds high key %q", page.id, bound, high)
		}
	}
	if high != nil {
		last := bounds[len(bounds)-1]
		if last == nil || !bytes.Equal(last, high) {
			return fmt.Errorf("blink: internal page %d rightmost bound does not equal high key", page.id)
		}
	}

	for i, child := range children {
		if child == nil {
			return fmt.Errorf("blink: internal page %d has nil child", page.id)
		}
		child.mu.RLock()
		childHigh := cloneBound(child.highKey)
		childLeaf := child.leaf
		childID := child.id
		child.mu.RUnlock()

		if bounds[i] != nil && childHigh != nil && bytes.Compare(childHigh, bounds[i]) > 0 {
			return fmt.Errorf("blink: child %d high key %q exceeds parent bound %q on page %d",
				childID, childHigh, bounds[i], page.id)
		}
		if childLeaf {
			if leaves[child] {
				return fmt.Errorf("blink: leaf page %d is reachable more than once", child.id)
			}
			leaves[child] = true
			continue
		}
		if err := t.validateInternal(child, seen, leaves); err != nil {
			return err
		}
	}
	return nil
}

func (n *node) deadPage() bool {
	n.mu.RLock()
	dead := n.dead
	n.mu.RUnlock()
	return dead
}
