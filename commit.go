package treekem

import (
	"bytes"
	"crypto/ecdh"
	"encoding/binary"
	"errors"
	"fmt"
)

// Recipient is one encrypted path-secret copy addressed to a resolution node.
type Recipient struct {
	Node            Index
	RecipientKey    []byte
	RecipientLeaves []int
	Envelope        *Envelope
}

// PathStep is one parent node on the committer's direct path.
type PathStep struct {
	Node                    Index
	EncryptionKey           []byte
	ParentHash              []byte
	OriginalChildResolution []Index
	Recipients              []Recipient
}

// UpdatePath is the committer's new leaf and one step per parent/root.
type UpdatePath struct {
	Committer         int
	LeafEncryptionKey []byte
	LeafSignatureKey  []byte
	LeafParentHash    []byte
	LeafSignature     []byte
	Steps             []PathStep
}

// Commit is a signed epoch proposal batch plus TreeKEM UpdatePath.
type Commit struct {
	GroupID      []byte
	Epoch        uint64
	Committer    int
	Proposals    []Proposal
	Path         *UpdatePath
	Confirmation []byte
	Signature    []byte
}

type builtPath struct {
	Path         *UpdatePath
	Secrets      [][]byte
	PrivateKeys  map[Index]*ecdh.PrivateKey
	CommitSecret []byte
}

func envelopeAAD(groupID []byte, epoch uint64, committer int, stepNode, recipientNode Index) []byte {
	hb := &hashBuilder{}
	hb.bytes([]byte("treekem-envelope-aad-v1"))
	hb.bytes(groupID)
	hb.uint64(epoch)
	hb.uint32(uint32(committer))
	hb.uint32(uint32(stepNode))
	hb.uint32(uint32(recipientNode))
	return hb.buf
}

func encodePathSecretPlaintext(stepNode Index, secret []byte) []byte {
	out := make([]byte, 4+len(secret))
	binary.BigEndian.PutUint32(out[:4], uint32(stepNode))
	copy(out[4:], secret)
	return out
}

func decodePathSecretPlaintext(plaintext []byte, expectedNode Index) ([]byte, error) {
	if len(plaintext) != 4+hashSize {
		return nil, fmt.Errorf("treekem: path plaintext has invalid length %d", len(plaintext))
	}
	node := Index(binary.BigEndian.Uint32(plaintext[:4]))
	if node != expectedNode {
		return nil, fmt.Errorf("treekem: path plaintext node %d != step node %d", node, expectedNode)
	}
	return append([]byte(nil), plaintext[4:]...), nil
}

func buildUpdatePath(
	tree *RatchetTree,
	leafIdentity *Identity,
	committerPosition int,
	groupID []byte,
	newEpoch uint64,
) (*builtPath, error) {
	leafIndex := Leaf(committerPosition)
	directPath, err := tree.DirectPath(leafIndex)
	if err != nil {
		return nil, err
	}
	copath, err := tree.Copath(leafIndex)
	if err != nil {
		return nil, err
	}
	if len(directPath) < 1 || len(copath) != len(directPath)-1 {
		return nil, errors.New("treekem: invalid direct/copath lengths")
	}
	stepCount := len(copath)

	secrets := make([][]byte, stepCount)
	privateKeys := make(map[Index]*ecdh.PrivateKey, stepCount)
	publicKeys := make([][]byte, stepCount)
	for i := 0; i < stepCount; i++ {
		if i == 0 {
			secrets[i], err = randomSecret()
			if err != nil {
				return nil, err
			}
		} else {
			secrets[i], err = derivePathSecret(secrets[i-1])
			if err != nil {
				return nil, err
			}
		}
		priv, public, err := derivePathPrivateKey(secrets[i], directPath[i+1])
		if err != nil {
			return nil, err
		}
		privateKeys[directPath[i+1]] = priv
		publicKeys[i] = public
	}

	resolutions := make([][]Index, stepCount)
	steps := make([]PathStep, stepCount)
	plannedParents := make([]*ParentNode, stepCount)
	for i := 0; i < stepCount; i++ {
		resolution, err := tree.Resolution(copath[i])
		if err != nil {
			return nil, fmt.Errorf("resolution for copath %d: %w", copath[i], err)
		}
		resolutions[i] = resolution
		recipients := make([]Recipient, 0, len(resolution))
		for _, recipientNode := range resolution {
			recipientKey, err := tree.PublicKeyAt(recipientNode)
			if err != nil {
				return nil, err
			}
			recipientLeaves, err := tree.ActiveLeavesUnder(recipientNode)
			if err != nil {
				return nil, err
			}
			aad := envelopeAAD(groupID, newEpoch, committerPosition, directPath[i+1], recipientNode)
			plaintext := encodePathSecretPlaintext(directPath[i+1], secrets[i])
			envelope, err := sealEnvelope(recipientKey, aad, plaintext)
			if err != nil {
				return nil, fmt.Errorf("seal to resolution node %d: %w", recipientNode, err)
			}
			recipients = append(recipients, Recipient{
				Node:            recipientNode,
				RecipientKey:    append([]byte(nil), recipientKey...),
				RecipientLeaves: recipientLeaves,
				Envelope:        envelope,
			})
		}

		plannedParents[i] = &ParentNode{
			EncryptionKey:           append([]byte(nil), publicKeys[i]...),
			UnmergedLeaves:          nil,
			OriginalChildResolution: append([]Index(nil), resolution...),
		}
		steps[i] = PathStep{
			Node:                    directPath[i+1],
			EncryptionKey:           append([]byte(nil), publicKeys[i]...),
			OriginalChildResolution: append([]Index(nil), resolution...),
			Recipients:              recipients,
		}
	}

	// Fill upward parent_hash fields from root downward.
	var nextParentHash []byte
	for i := stepCount - 1; i >= 0; i-- {
		plannedParents[i].ParentHash = append([]byte(nil), nextParentHash...)
		nextParentHash = ComputeParentHash(directPath[i+1], plannedParents[i])
	}
	for i := range steps {
		steps[i].ParentHash = append([]byte(nil), plannedParents[i].ParentHash...)
	}
	leafParentHash := append([]byte(nil), nextParentHash...)

	rawLeaf, err := tree.node(leafIndex)
	if err != nil {
		return nil, err
	}
	if rawLeaf.Blank() || rawLeaf.Leaf == nil {
		return nil, fmt.Errorf("treekem: committer leaf %d is blank", committerPosition)
	}
	rawLeaf.Leaf.ParentHash = leafParentHash
	if leafIdentity != nil {
		if err := signLeaf(leafIdentity, rawLeaf.Leaf); err != nil {
			return nil, err
		}
	}

	path := &UpdatePath{
		Committer:         committerPosition,
		LeafEncryptionKey: append([]byte(nil), rawLeaf.Leaf.EncryptionKey...),
		LeafSignatureKey:  append([]byte(nil), rawLeaf.Leaf.SignatureKey...),
		LeafParentHash:    append([]byte(nil), leafParentHash...),
		LeafSignature:     append([]byte(nil), rawLeaf.Leaf.Signature...),
		Steps:             steps,
	}

	commitSecret, err := deriveCommitSecret(secrets[len(secrets)-1])
	if err != nil {
		return nil, err
	}
	return &builtPath{Path: path, Secrets: secrets, PrivateKeys: privateKeys, CommitSecret: commitSecret}, nil
}

func equalIndexes(a, b []Index) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func equalInts(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// ValidatePathRecipients checks every path step against the exact bounded
// copath resolution after proposals but before installing the new path. It
// rejects extra envelopes, ghost recipient indexes, stale recipient keys, and
// metadata that includes a blank/removed leaf.
func ValidatePathRecipients(tree *RatchetTree, commit *Commit) error {
	if commit == nil || commit.Path == nil {
		return errors.New("treekem: commit has no update path")
	}
	path := commit.Path
	directPath, err := tree.DirectPath(Leaf(commit.Committer))
	if err != nil {
		return err
	}
	copath, err := tree.Copath(Leaf(commit.Committer))
	if err != nil {
		return err
	}
	if len(path.Steps) != len(copath) {
		return fmt.Errorf("treekem: path has %d steps, want %d", len(path.Steps), len(copath))
	}

	for i, step := range path.Steps {
		expectedNode := directPath[i+1]
		if step.Node != expectedNode {
			return fmt.Errorf("treekem: step %d node %d != direct path node %d", i, step.Node, expectedNode)
		}
		resolution, err := tree.Resolution(copath[i])
		if err != nil {
			return err
		}
		if !equalIndexes(step.OriginalChildResolution, resolution) {
			return fmt.Errorf("treekem: step %d original_child_resolution %v != resolution %v", i, step.OriginalChildResolution, resolution)
		}
		gotRecipients := make([]Index, 0, len(step.Recipients))
		for _, recipient := range step.Recipients {
			gotRecipients = append(gotRecipients, recipient.Node)
		}
		if !equalIndexes(gotRecipients, resolution) {
			return fmt.Errorf("treekem: step %d recipients %v != exact resolution %v", i, gotRecipients, resolution)
		}

		for _, recipient := range step.Recipients {
			publicKey, err := tree.PublicKeyAt(recipient.Node)
			if err != nil {
				return fmt.Errorf("step %d recipient %d: %w", i, recipient.Node, err)
			}
			if !bytes.Equal(publicKey, recipient.RecipientKey) {
				return fmt.Errorf("treekem: step %d recipient %d key does not match tree", i, recipient.Node)
			}
			leafSet, err := tree.ActiveLeavesUnder(recipient.Node)
			if err != nil {
				return err
			}
			if !equalInts(recipient.RecipientLeaves, leafSet) {
				return fmt.Errorf("treekem: step %d recipient %d leaf set %v != active leaves %v", i, recipient.Node, recipient.RecipientLeaves, leafSet)
			}
			for _, leafPosition := range recipient.RecipientLeaves {
				leafIndex := Leaf(leafPosition)
				raw, err := tree.node(leafIndex)
				if err != nil {
					return err
				}
				if raw.Blank() {
					return fmt.Errorf("treekem: removed/blank leaf %d remains a recipient", leafPosition)
				}
			}
		}
	}
	return nil
}

// AllRecipientLeaves returns the deduplicated active leaf positions named in
// all encrypted path steps.
func (c *Commit) AllRecipientLeaves() []int {
	seen := map[int]bool{}
	out := []int{}
	if c == nil || c.Path == nil {
		return out
	}
	for _, step := range c.Path.Steps {
		for _, recipient := range step.Recipients {
			for _, leaf := range recipient.RecipientLeaves {
				if !seen[leaf] {
					seen[leaf] = true
					out = append(out, leaf)
				}
			}
		}
	}
	return out
}

// ExplicitlyRejectsLeaf reports whether a leaf appears in any recipient leaf set.
func (c *Commit) IncludesRecipientLeaf(position int) bool {
	for _, leaf := range c.AllRecipientLeaves() {
		if leaf == position {
			return true
		}
	}
	return false
}

func installUpdatePath(tree *RatchetTree, path *UpdatePath) error {
	if path == nil {
		return errors.New("treekem: nil update path")
	}
	leafIndex := Leaf(path.Committer)
	rawLeaf, err := tree.node(leafIndex)
	if err != nil {
		return err
	}
	if rawLeaf.Blank() || rawLeaf.Leaf == nil {
		return fmt.Errorf("treekem: cannot install path at blank leaf %d", path.Committer)
	}
	rawLeaf.Leaf.EncryptionKey = append([]byte(nil), path.LeafEncryptionKey...)
	rawLeaf.Leaf.SignatureKey = append([]byte(nil), path.LeafSignatureKey...)
	rawLeaf.Leaf.ParentHash = append([]byte(nil), path.LeafParentHash...)
	rawLeaf.Leaf.Signature = append([]byte(nil), path.LeafSignature...)

	for _, step := range path.Steps {
		if !tree.ValidIndex(step.Node) || IsLeaf(step.Node) {
			return fmt.Errorf("%w: path step node %d", ErrInvalidIndex, step.Node)
		}
		tree.Nodes[step.Node] = &TreeNode{
			Index: step.Node,
			Parent: &ParentNode{
				EncryptionKey:           append([]byte(nil), step.EncryptionKey...),
				ParentHash:              append([]byte(nil), step.ParentHash...),
				UnmergedLeaves:          nil,
				OriginalChildResolution: append([]Index(nil), step.OriginalChildResolution...),
			},
		}
	}
	return tree.ValidateShape()
}

// DecryptedPath contains a recipient's recovered path secrets and private keys.
type DecryptedPath struct {
	FirstStep    int
	Secrets      [][]byte
	PrivateKeys  map[Index]*ecdh.PrivateKey
	CommitSecret []byte
}

func decryptUpdatePath(
	tree *RatchetTree,
	commit *Commit,
	ring map[Index]*ecdh.PrivateKey,
) (*DecryptedPath, error) {
	if err := ValidatePathRecipients(tree, commit); err != nil {
		return nil, err
	}
	path := commit.Path
	stepCount := len(path.Steps)
	secrets := make([][]byte, stepCount)
	privateKeys := map[Index]*ecdh.PrivateKey{}

	for stepIndex, step := range path.Steps {
		for _, recipient := range step.Recipients {
			privateKey := ring[recipient.Node]
			if privateKey == nil {
				continue
			}
			aad := envelopeAAD(commit.GroupID, commit.Epoch, commit.Committer, step.Node, recipient.Node)
			plaintext, err := openEnvelope(privateKey, aad, recipient.Envelope)
			if err != nil {
				continue
			}
			secret, err := decodePathSecretPlaintext(plaintext, step.Node)
			if err != nil {
				continue
			}
			secrets[stepIndex] = secret

			for j := stepIndex; j < stepCount; j++ {
				if j > stepIndex {
					secret, err = derivePathSecret(secret)
					if err != nil {
						return nil, err
					}
				}
				secrets[j] = append([]byte(nil), secret...)
				priv, _, err := derivePathPrivateKey(secret, path.Steps[j].Node)
				if err != nil {
					return nil, err
				}
				privateKeys[path.Steps[j].Node] = priv
			}
			commitSecret, err := deriveCommitSecret(secrets[stepCount-1])
			if err != nil {
				return nil, err
			}
			return &DecryptedPath{
				FirstStep:    stepIndex,
				Secrets:      secrets,
				PrivateKeys:  privateKeys,
				CommitSecret: commitSecret,
			}, nil
		}
	}
	return nil, errors.New("treekem: no path-secret envelope was decryptable")
}

// TryDecryptPathForLeaf is the host-aware wrapper that treats leafPosition's
// long-term private key as available even though it is not yet in the ring.
func TryDecryptPathForLeaf(
	tree *RatchetTree,
	commit *Commit,
	leafPosition int,
	leafPrivate *ecdh.PrivateKey,
	ring map[Index]*ecdh.PrivateKey,
) (*DecryptedPath, error) {
	wrappedRing := map[Index]*ecdh.PrivateKey{}
	for index, key := range ring {
		wrappedRing[index] = key
	}
	wrappedRing[Leaf(leafPosition)] = leafPrivate
	return decryptUpdatePath(tree, commit, wrappedRing)
}
