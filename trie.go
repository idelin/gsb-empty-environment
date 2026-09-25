package triestore

import (
	"bytes"
	"encoding/hex"
	"errors"
	"fmt"
)

// Get returns the value stored at key, or found=false.
func (s *Store) Get(key []byte) (value []byte, found bool, err error) {
	return s.getFrom(s.root, key)
}

// GetAt reads key from a historical root snapshot.
func (s *Store) GetAt(root Root, key []byte) (value []byte, found bool, err error) {
	return s.getFrom(root.ID, key)
}

func (s *Store) getFrom(rootID string, key []byte) ([]byte, bool, error) {
	id := rootID
	for depth := 0; ; depth++ {
		if id == emptyID {
			return nil, false, nil
		}
		n, err := s.loadNode(id)
		if err != nil {
			return nil, false, err
		}
		if depth == len(key) {
			if !n.hasValue {
				return nil, false, nil
			}
			return append([]byte(nil), n.value...), true, nil
		}
		id = n.children[key[depth]]
	}
}

// Put inserts or replaces key->value and commits a new root snapshot.
// Only the nodes along key's path are copied; every other subtree is shared
// with the previous root by reference.
func (s *Store) Put(key, value []byte) (Root, error) {
	newRoot, err := s.putRec(s.root, key, 0, value)
	if err != nil {
		return Root{}, err
	}
	s.root = newRoot
	return s.Commit()
}

// putRec returns the ID of the new subtree for key[depth:].
func (s *Store) putRec(id string, key []byte, depth int, value []byte) (string, error) {
	n := newNode()
	if id != emptyID {
		old, err := s.loadNode(id)
		if err != nil {
			return "", err
		}
		n = old.clone()
	}
	if depth == len(key) {
		n.hasValue = true
		n.value = append([]byte(nil), value...)
		return s.writeNode(n)
	}
	b := key[depth]
	childID, err := s.putRec(n.children[b], key, depth+1, value)
	if err != nil {
		return "", err
	}
	n.children[b] = childID
	return s.writeNode(n)
}

// Delete removes key and commits a new root snapshot. Empty branches are
// compressed away: a node with no value and no children is dropped from its
// parent, so deleting "ab" prunes its branch but never touches "abc".
// found=false means the key was not present (the root is left unchanged and
// no snapshot is committed).
func (s *Store) Delete(key []byte) (found bool, err error) {
	newRoot, _, found, err := s.delRec(s.root, key, 0)
	if err != nil || !found {
		return found, err
	}
	s.root = newRoot
	_, err = s.Commit()
	return true, err
}

// delRec returns (newID, empty, found, err). empty=true means the subtree
// became valueless and childless and should be unlinked by the parent.
func (s *Store) delRec(id string, key []byte, depth int) (string, bool, bool, error) {
	if id == emptyID {
		return emptyID, true, false, nil
	}
	old, err := s.loadNode(id)
	if err != nil {
		return "", false, false, err
	}
	n := old.clone()
	if depth == len(key) {
		if !n.hasValue {
			return id, false, false, nil
		}
		n.hasValue = false
		n.value = nil
	} else {
		b := key[depth]
		childID, childEmpty, found, err := s.delRec(n.children[b], key, depth+1)
		if err != nil || !found {
			return id, false, found, err
		}
		if childEmpty {
			delete(n.children, b) // compression: prune the dead branch
		} else {
			n.children[b] = childID
		}
	}
	if !n.hasValue && len(n.children) == 0 {
		return emptyID, true, true, nil
	}
	newID, err := s.writeNode(n)
	return newID, false, true, err
}

// Scan iterates all key/value pairs whose key starts with prefix, in
// lexicographic byte order. Returning false from fn stops the scan early.
func (s *Store) Scan(prefix []byte, fn func(key, value []byte) bool) error {
	return s.scanFrom(s.root, prefix, fn)
}

// ScanAt is Scan against a historical root snapshot.
func (s *Store) ScanAt(root Root, prefix []byte, fn func(key, value []byte) bool) error {
	return s.scanFrom(root.ID, prefix, fn)
}

func (s *Store) scanFrom(rootID string, prefix []byte, fn func(key, value []byte) bool) error {
	id := rootID
	for depth := 0; depth < len(prefix); depth++ {
		if id == emptyID {
			return nil
		}
		n, err := s.loadNode(id)
		if err != nil {
			return err
		}
		id = n.children[prefix[depth]]
	}
	if id == emptyID {
		return nil
	}
	if err := s.walk(id, append([]byte(nil), prefix...), fn); err != errStop {
		return err
	}
	return nil
}

func (s *Store) walk(id string, key []byte, fn func(key, value []byte) bool) error {
	n, err := s.loadNode(id)
	if err != nil {
		return err
	}
	if n.hasValue && !fn(key, n.value) {
		return errStop
	}
	for b := 0; b < 256; b++ {
		childID, ok := n.children[byte(b)]
		if !ok {
			continue
		}
		if err := s.walk(childID, append(append([]byte(nil), key...), byte(b)), fn); err != nil {
			return err
		}
	}
	return nil
}

var errStop = errors.New("triestore: scan stopped")

// Verify walks the whole tree reachable from the current root and checks
// every node's CRC and content hash. A corruption is reported with the node
// ID and the key path at which it was referenced.
func (s *Store) Verify() error { return s.VerifyRoot(Root{Seq: s.seq, ID: s.root}) }

// VerifyRoot verifies the tree reachable from an arbitrary root snapshot.
func (s *Store) VerifyRoot(root Root) error {
	err := s.verifyRec(root.ID, nil)
	if err == errStop {
		return nil
	}
	return err
}

func (s *Store) verifyRec(id string, path []byte) error {
	if id == emptyID {
		return nil
	}
	n, err := s.loadNode(id)
	if err != nil {
		return fmt.Errorf("verify: at key 0x%s: %w", hex.EncodeToString(path), err)
	}
	for b := 0; b < 256; b++ {
		childID, ok := n.children[byte(b)]
		if !ok {
			continue
		}
		if err := s.verifyRec(childID, append(append([]byte(nil), path...), byte(b))); err != nil {
			return err
		}
	}
	return nil
}

// Keys is a convenience helper collecting all keys under prefix.
func (s *Store) Keys(prefix []byte) ([][]byte, error) {
	var out [][]byte
	err := s.Scan(prefix, func(k, v []byte) bool {
		out = append(out, append([]byte(nil), k...))
		return true
	})
	return out, err
}

// EqualKeys reports whether got matches want byte-for-byte; test helper.
func EqualKeys(got [][]byte, want ...string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if !bytes.Equal(got[i], []byte(want[i])) {
			return false
		}
	}
	return true
}
