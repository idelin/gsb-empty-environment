package treekem

import (
	"crypto/ed25519"
	"errors"
	"fmt"
)

// KeyPackage is a signed pre-add identity bundle. Its leaf signature is valid
// for a leaf whose parent_hash field is still empty.
type KeyPackage struct {
	Public    PublicIdentity `json:"public"`
	Signature []byte         `json:"signature"`
}

func keyPackageSigningBytes(public PublicIdentity) []byte {
	hb := &hashBuilder{}
	hb.bytes([]byte(public.ID))
	hb.bytes(public.EncryptionKey)
	hb.bytes(public.SignatureKey)
	hb.bytes(nil) // parent_hash is absent before the leaf joins a tree/path.
	return hb.sum("treekem-leaf-node-v1")
}

// KeyPackage creates the signed public identity used by Add proposals.
func (i *Identity) KeyPackage() (*KeyPackage, error) {
	public := i.Public()
	message := keyPackageSigningBytes(public)
	return &KeyPackage{Public: public, Signature: i.Sign(message)}, nil
}

// VerifyKeyPackage validates the self-signature on an Add identity bundle.
func VerifyKeyPackage(keyPackage *KeyPackage) error {
	if keyPackage == nil {
		return errors.New("treekem: nil key package")
	}
	if len(keyPackage.Public.SignatureKey) != ed25519.PublicKeySize {
		return fmt.Errorf("treekem: invalid signature key length %d", len(keyPackage.Public.SignatureKey))
	}
	if len(keyPackage.Public.EncryptionKey) != 32 {
		return fmt.Errorf("treekem: invalid encryption key length %d", len(keyPackage.Public.EncryptionKey))
	}
	message := keyPackageSigningBytes(keyPackage.Public)
	if !verifySignature(ed25519.PublicKey(keyPackage.Public.SignatureKey), message, keyPackage.Signature) {
		return errors.New("treekem: invalid key package signature")
	}
	return nil
}

func leafSigningBytes(leaf *LeafNode) []byte {
	hb := &hashBuilder{}
	hb.bytes([]byte(leaf.ID))
	hb.bytes(leaf.EncryptionKey)
	hb.bytes(leaf.SignatureKey)
	hb.bytes(leaf.ParentHash)
	return hb.sum("treekem-leaf-node-v1")
}

func signLeaf(identity *Identity, leaf *LeafNode) error {
	if identity == nil {
		return errors.New("treekem: missing signing identity")
	}
	if leaf.ID != identity.public.ID {
		return fmt.Errorf("treekem: identity %q cannot sign leaf %q", identity.public.ID, leaf.ID)
	}
	leaf.Signature = identity.Sign(leafSigningBytes(leaf))
	return nil
}

// VerifyLeafSignature validates the Ed25519 signature over a LeafNode and its
// parent_hash field.
func VerifyLeafSignature(leaf *LeafNode) error {
	if leaf == nil {
		return errors.New("treekem: nil leaf")
	}
	if len(leaf.SignatureKey) != ed25519.PublicKeySize {
		return fmt.Errorf("treekem: leaf %d has invalid signature key", leaf.Position)
	}
	if !verifySignature(ed25519.PublicKey(leaf.SignatureKey), leafSigningBytes(leaf), leaf.Signature) {
		return fmt.Errorf("treekem: invalid signature for leaf %d (%s)", leaf.Position, leaf.ID)
	}
	return nil
}

// ComputeParentHash hashes the RFC 9420 ParentHashInput-style binding of a
// parent public key, its upward parent hash, and the original child resolution.
// The node index is included as an unambiguous position binder.
func ComputeParentHash(nodeIndex Index, parent *ParentNode) []byte {
	hb := &hashBuilder{}
	hb.uint32(uint32(nodeIndex))
	if parent == nil {
		hb.byte(0)
	} else {
		hb.byte(1)
		hb.bytes(parent.EncryptionKey)
		hb.bytes(parent.ParentHash)
		hb.indexes(parent.OriginalChildResolution)
	}
	return hb.sum("MLS 1.0 Parent Hash")
}

// VerifyParentHashChain verifies one leaf's recursive chain to the root. It
// returns the verified parent indexes in direct-path order.
func VerifyParentHashChain(tree *RatchetTree, leafPosition int) ([]Index, error) {
	if tree == nil {
		return nil, errors.New("treekem: nil tree")
	}
	leafIndex := Leaf(leafPosition)
	path, err := tree.DirectPath(leafIndex)
	if err != nil {
		return nil, err
	}
	leafNode, err := tree.node(leafIndex)
	if err != nil {
		return nil, err
	}
	if leafNode.Blank() || leafNode.Leaf == nil {
		return nil, fmt.Errorf("treekem: leaf %d is blank", leafPosition)
	}

	expectedHash := append([]byte(nil), leafNode.Leaf.ParentHash...)
	verified := []Index{}
	for i := 1; i < len(path); i++ {
		parentIndex := path[i]
		rawParent, err := tree.node(parentIndex)
		if err != nil {
			return verified, err
		}
		if rawParent.Blank() || rawParent.Parent == nil {
			return verified, fmt.Errorf("%w: expected non-blank parent %d on chain from leaf %d", ErrGhostParent, parentIndex, leafPosition)
		}
		actualHash := ComputeParentHash(parentIndex, rawParent.Parent)
		if !constantTimeEqual(expectedHash, actualHash) {
			return verified, fmt.Errorf("%w at node %d from leaf %d", ErrParentHashMismatch, parentIndex, leafPosition)
		}
		verified = append(verified, parentIndex)
		expectedHash = append([]byte(nil), rawParent.Parent.ParentHash...)
	}

	rootIndex := path[len(path)-1]
	rootNode, err := tree.node(rootIndex)
	if err != nil {
		return verified, err
	}
	if rootNode.Parent != nil && len(rootNode.Parent.ParentHash) != 0 {
		prefix := verified
		if len(prefix) > 0 && prefix[len(prefix)-1] == rootIndex {
			prefix = prefix[:len(prefix)-1]
		}
		return prefix, fmt.Errorf("%w: root %d has non-empty upward parent hash", ErrParentHashMismatch, rootIndex)
	}
	if len(expectedHash) != 0 {
		prefix := verified
		if len(prefix) > 0 && prefix[len(prefix)-1] == rootIndex {
			prefix = prefix[:len(prefix)-1]
		}
		return prefix, fmt.Errorf("%w: dangling parent hash above root %d", ErrParentHashMismatch, rootIndex)
	}
	return verified, nil
}

// VerifyLeafSignatures validates every non-blank signed LeafNode.
func VerifyLeafSignatures(tree *RatchetTree) error {
	if err := tree.ValidateShape(); err != nil {
		return err
	}
	for position := 0; position < tree.LeafCount; position++ {
		x := Leaf(position)
		if int(x) >= len(tree.Nodes) || tree.Nodes[x] == nil || tree.Nodes[x].Leaf == nil {
			continue
		}
		if err := VerifyLeafSignature(tree.Nodes[x].Leaf); err != nil {
			return err
		}
	}
	return nil
}

// VerifyParentHashes performs the whole-tree RFC 9420 §7.9 invariant check.
// Every non-blank parent must be witnessed by at least one signed descendant
// leaf whose recursive hash chain validates all the way to the root. Leaf
// signatures are checked first so a malicious delivery service cannot forge a
// leaf parent_hash to certify an injected ghost key.
func VerifyParentHashes(tree *RatchetTree) error {
	if tree == nil {
		return errors.New("treekem: nil tree")
	}
	if err := VerifyLeafSignatures(tree); err != nil {
		return err
	}
	if err := tree.ValidateShape(); err != nil {
		return err
	}

	witnessed := map[Index]bool{}
	for position := 0; position < tree.LeafCount; position++ {
		leafIndex := Leaf(position)
		if int(leafIndex) >= len(tree.Nodes) || tree.Nodes[leafIndex] == nil || tree.Nodes[leafIndex].Leaf == nil {
			continue
		}
		if len(tree.Nodes[leafIndex].Leaf.ParentHash) == 0 {
			continue
		}
		parents, chainErr := VerifyParentHashChain(tree, position)
		for _, parentIndex := range parents {
			witnessed[parentIndex] = true
		}
		if chainErr != nil {
			// Historical leaf hashes can be superseded by a later committer.
			// Such a stale chain is not a current witness, but tampering with
			// the current witness removes the only witness for its parents and
			// is rejected below.
			if errors.Is(chainErr, ErrParentHashMismatch) || errors.Is(chainErr, ErrGhostParent) {
				continue
			}
			return chainErr
		}
	}

	for x, rawNode := range tree.Nodes {
		if rawNode.Blank() || rawNode.Parent == nil {
			continue
		}
		if !witnessed[Index(x)] {
			return fmt.Errorf("%w: non-blank parent %d has no signed descendant parent-hash chain", ErrGhostParent, x)
		}
	}
	return nil
}

var (
	// ErrParentHashMismatch indicates a broken/tampered parent-hash chain.
	ErrParentHashMismatch = errors.New("treekem: parent hash verification failed")
	// ErrGhostParent indicates an unwitnessed or out-of-tree parent key.
	ErrGhostParent = errors.New("treekem: ghost parent node")
	// ErrTreeHashMismatch indicates the public tree differs from its trusted hash.
	ErrTreeHashMismatch = errors.New("treekem: tree hash mismatch")
)

// VerifyCommittedTree checks the committer's complete root chain and then the
// whole-tree parent-witness invariant. The explicit chain rejects a DS that
// blanks a parent required by the current UpdatePath; VerifyParentHashes rejects
// injected unwitnessed parent keys elsewhere.
func VerifyCommittedTree(tree *RatchetTree, committerPosition int) error {
	if _, err := VerifyParentHashChain(tree, committerPosition); err != nil {
		return err
	}
	return VerifyParentHashes(tree)
}

// VerifyTree additionally anchors the complete public tree to expectedTreeHash,
// as carried by a signed GroupContext/Welcome. This detects malicious blanking
// of historical parent nodes even when no current parent-hash witness remains.
func VerifyTree(tree *RatchetTree, expectedTreeHash []byte) error {
	if err := VerifyParentHashes(tree); err != nil {
		return err
	}
	if len(expectedTreeHash) > 0 && !constantTimeEqual(tree.Hash(), expectedTreeHash) {
		return ErrTreeHashMismatch
	}
	return nil
}
