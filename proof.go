package mpt

import (
	"bytes"
	"errors"
	"fmt"
)

// ProofStep is one node on the path from the root to a leaf, together with
// its Merkle hash. A proof is the full ordered sequence of such steps.
type ProofStep struct {
	Node Node
	Hash []byte
}

// Prove returns the Merkle proof for key: the complete sequence of nodes
// from the root down to the leaf (or terminating node) for that key.
// The boolean reports whether the key exists in the trie.
func (t *Trie) Prove(key string) ([]ProofStep, bool) {
	var steps []ProofStep
	node := t.root
	rest := keyToNibbles(key)
	for node != nil {
		steps = append(steps, ProofStep{Node: node, Hash: node.Hash()})
		switch n := node.(type) {
		case *LeafNode:
			return steps, bytes.Equal(n.Path, rest)
		case *ExtensionNode:
			if len(rest) < len(n.Path) || !bytes.Equal(n.Path, rest[:len(n.Path)]) {
				return steps, false
			}
			rest = rest[len(n.Path):]
			node = n.Child
		case *BranchNode:
			if len(rest) == 0 {
				return steps, n.Value != nil
			}
			node = n.Children[rest[0]]
			rest = rest[1:]
		}
	}
	return steps, false
}

// VerifyProof checks a proof against an expected root hash and returns the
// value bound to key. It fails if:
//   - the first step's hash does not equal the expected root hash,
//   - any node's stored hash does not match its recomputed content hash,
//   - any parent does not reference the hash of the next step (broken chain),
//   - the consumed key nibbles do not equal key, or the key is absent.
//
// Because every parent hash commits to its child hashes, tampering with any
// parent hash breaks the chain and is detected here.
func VerifyProof(rootHash []byte, key string, steps []ProofStep) ([]byte, error) {
	if len(steps) == 0 {
		return nil, errors.New("mpt: empty proof")
	}
	if !bytes.Equal(steps[0].Hash, rootHash) {
		return nil, errors.New("mpt: proof root hash does not match expected root hash")
	}

	// 1. Every stored hash must equal the recomputed content hash.
	for i, s := range steps {
		if !bytes.Equal(s.Node.Hash(), s.Hash) {
			return nil, fmt.Errorf("mpt: step %d: node content does not match its hash (tampered node)", i)
		}
	}

	// 2. Every parent must cryptographically reference the next step.
	for i := 0; i+1 < len(steps); i++ {
		childHash := steps[i+1].Hash
		switch p := steps[i].Node.(type) {
		case *ExtensionNode:
			if !bytes.Equal(p.Child.Hash(), childHash) {
				return nil, fmt.Errorf("mpt: step %d: extension does not reference child hash (broken chain)", i)
			}
		case *BranchNode:
			found := false
			for _, c := range p.Children {
				if c != nil && bytes.Equal(c.Hash(), childHash) {
					found = true
					break
				}
			}
			if !found {
				return nil, fmt.Errorf("mpt: step %d: branch does not reference child hash (broken chain)", i)
			}
		default:
			return nil, fmt.Errorf("mpt: step %d: leaf cannot have descendants", i)
		}
	}

	// 3. Replay the key nibble path against the proof steps.
	rest := keyToNibbles(key)
	for i, s := range steps {
		last := i == len(steps)-1
		switch n := s.Node.(type) {
		case *ExtensionNode:
			if len(rest) < len(n.Path) || !bytes.Equal(n.Path, rest[:len(n.Path)]) {
				return nil, fmt.Errorf("mpt: step %d: extension path diverges from key", i)
			}
			rest = rest[len(n.Path):]
		case *BranchNode:
			if len(rest) == 0 {
				if !last || n.Value == nil {
					return nil, errors.New("mpt: key terminates at branch without value")
				}
				return n.Value, nil
			}
			if last {
				return nil, errors.New("mpt: proof ends before key is consumed")
			}
			// the next step must be the child at the next nibble
			child := n.Children[rest[0]]
			if child == nil || !bytes.Equal(child.Hash(), steps[i+1].Hash) {
				return nil, fmt.Errorf("mpt: step %d: branch child at nibble %x does not lead to next step", i, rest[0])
			}
			rest = rest[1:]
		case *LeafNode:
			if !last {
				return nil, fmt.Errorf("mpt: step %d: leaf in the middle of a proof", i)
			}
			if !bytes.Equal(n.Path, rest) {
				return nil, errors.New("mpt: leaf path does not match remaining key")
			}
			return n.Value, nil
		}
	}
	return nil, errors.New("mpt: proof does not resolve key")
}
