package treekem

import (
	"errors"
	"fmt"
)

// ProposalKind enumerates the RFC 9420 proposal types modeled here.
type ProposalKind string

const (
	ProposalAdd    ProposalKind = "add"
	ProposalRemove ProposalKind = "remove"
	ProposalUpdate ProposalKind = "update"
)

// Proposal is a validated Add/Remove/Update proposal. Add and Update carry a
// self-signed KeyPackage with fresh public key material.
type Proposal struct {
	Kind               ProposalKind
	Add                *KeyPackage
	Update             *UpdateProposal
	RemoveLeafPosition int
}

// UpdateProposal rotates a leaf's public key material.
type UpdateProposal struct {
	LeafPosition int
	KeyPackage   *KeyPackage
}

// NewAddProposal creates an Add proposal from a signed KeyPackage.
func NewAddProposal(keyPackage *KeyPackage) (Proposal, error) {
	if err := VerifyKeyPackage(keyPackage); err != nil {
		return Proposal{}, err
	}
	return Proposal{Kind: ProposalAdd, Add: keyPackage}, nil
}

// NewRemoveProposal creates a Remove proposal for a leaf position.
func NewRemoveProposal(leafPosition int) Proposal {
	return Proposal{Kind: ProposalRemove, RemoveLeafPosition: leafPosition}
}

// NewUpdateProposal rotates the key material at leafPosition.
func NewUpdateProposal(leafPosition int, keyPackage *KeyPackage) (Proposal, error) {
	if err := VerifyKeyPackage(keyPackage); err != nil {
		return Proposal{}, err
	}
	return Proposal{Kind: ProposalUpdate, Update: &UpdateProposal{LeafPosition: leafPosition, KeyPackage: keyPackage}}, nil
}

// AppliedProposals records resulting leaf positions.
type AppliedProposals struct {
	AddedLeafPositions   []int
	RemovedLeafPositions []int
	UpdatedLeafPositions []int
}

// ApplyProposals applies proposals in order to tree. The caller normally passes
// a Clone so a malformed/untrusted Commit cannot mutate live state early.
func ApplyProposals(tree *RatchetTree, proposals []Proposal) (*AppliedProposals, error) {
	if tree == nil {
		return nil, errors.New("treekem: nil tree")
	}
	if err := tree.ValidateShape(); err != nil {
		return nil, err
	}
	result := &AppliedProposals{}

	for proposalIndex, proposal := range proposals {
		switch proposal.Kind {
		case ProposalAdd:
			position, err := applyAdd(tree, proposal.Add)
			if err != nil {
				return nil, fmt.Errorf("proposal %d add: %w", proposalIndex, err)
			}
			result.AddedLeafPositions = append(result.AddedLeafPositions, position)

		case ProposalRemove:
			if err := applyRemove(tree, proposal.RemoveLeafPosition); err != nil {
				return nil, fmt.Errorf("proposal %d remove: %w", proposalIndex, err)
			}
			result.RemovedLeafPositions = append(result.RemovedLeafPositions, proposal.RemoveLeafPosition)

		case ProposalUpdate:
			if err := applyUpdate(tree, proposal.Update); err != nil {
				return nil, fmt.Errorf("proposal %d update: %w", proposalIndex, err)
			}
			result.UpdatedLeafPositions = append(result.UpdatedLeafPositions, proposal.Update.LeafPosition)

		default:
			return nil, fmt.Errorf("proposal %d: unknown kind %q", proposalIndex, proposal.Kind)
		}
	}

	if err := tree.ValidateShape(); err != nil {
		return nil, err
	}
	return result, nil
}

func activeIdentityExists(tree *RatchetTree, id string) bool {
	for position := 0; position < tree.LeafCount; position++ {
		if memberID, ok := tree.MemberAt(position); ok && memberID == id {
			return true
		}
	}
	return false
}

func applyAdd(tree *RatchetTree, keyPackage *KeyPackage) (int, error) {
	if err := VerifyKeyPackage(keyPackage); err != nil {
		return -1, err
	}
	if keyPackage.Public.ID == "" {
		return -1, errors.New("member ID is empty")
	}
	if activeIdentityExists(tree, keyPackage.Public.ID) {
		return -1, fmt.Errorf("duplicate active member ID %q", keyPackage.Public.ID)
	}

	position := tree.LeafCount
	tree.LeafCount++
	for len(tree.Nodes) < Width(tree.LeafCount) {
		newIndex := Index(len(tree.Nodes))
		tree.Nodes = append(tree.Nodes, &TreeNode{Index: newIndex})
	}

	leafIndex := Leaf(position)
	tree.Nodes[leafIndex] = &TreeNode{
		Index: leafIndex,
		Leaf: &LeafNode{
			Position:      position,
			ID:            keyPackage.Public.ID,
			EncryptionKey: append([]byte(nil), keyPackage.Public.EncryptionKey...),
			SignatureKey:  append([]byte(nil), keyPackage.Public.SignatureKey...),
			ParentHash:    nil,
			Signature:     append([]byte(nil), keyPackage.Signature...),
		},
	}

	path, err := tree.DirectPath(leafIndex)
	if err != nil {
		return -1, err
	}
	for _, parentIndex := range path[1:] {
		parentNode := tree.Nodes[parentIndex]
		if parentNode != nil && parentNode.Parent != nil {
			if !containsInt(parentNode.Parent.UnmergedLeaves, position) {
				parentNode.Parent.UnmergedLeaves = append(parentNode.Parent.UnmergedLeaves, position)
			}
		}
	}
	return position, nil
}

func applyRemove(tree *RatchetTree, position int) error {
	if position < 0 || position >= tree.LeafCount {
		return fmt.Errorf("%w: remove leaf %d", ErrInvalidIndex, position)
	}
	leafIndex := Leaf(position)
	leafNode := tree.Nodes[leafIndex]
	if leafNode.Blank() || leafNode.Leaf == nil {
		return fmt.Errorf("treekem: leaf %d is already blank", position)
	}

	path, err := tree.DirectPath(leafIndex)
	if err != nil {
		return err
	}

	// Blank the leaf and every parent key on its direct path. Those are exactly
	// the non-leaf keys the removed member could have possessed. The committer's
	// fresh UpdatePath repopulates the intersection with the committer path;
	// resolution then bypasses every other purged parent to surviving leaf keys.
	for _, nodeIndex := range path {
		tree.Nodes[nodeIndex] = &TreeNode{Index: nodeIndex}
	}

	// Remove the position from any surviving unmerged lists as well. Normally
	// all ancestors listing it were blanked above; this keeps malformed or
	// partially constructed trees canonical.
	for _, rawNode := range tree.Nodes {
		if rawNode == nil || rawNode.Parent == nil {
			continue
		}
		filtered := rawNode.Parent.UnmergedLeaves[:0]
		for _, unmerged := range rawNode.Parent.UnmergedLeaves {
			if unmerged != position {
				filtered = append(filtered, unmerged)
			}
		}
		rawNode.Parent.UnmergedLeaves = filtered
	}
	return nil
}

func applyUpdate(tree *RatchetTree, update *UpdateProposal) error {
	if update == nil || update.KeyPackage == nil {
		return errors.New("missing update body")
	}
	position := update.LeafPosition
	if position < 0 || position >= tree.LeafCount {
		return fmt.Errorf("%w: update leaf %d", ErrInvalidIndex, position)
	}
	leafIndex := Leaf(position)
	rawNode := tree.Nodes[leafIndex]
	if rawNode.Blank() || rawNode.Leaf == nil {
		return fmt.Errorf("treekem: cannot update blank leaf %d", position)
	}
	if err := VerifyKeyPackage(update.KeyPackage); err != nil {
		return err
	}
	if update.KeyPackage.Public.ID != rawNode.Leaf.ID {
		return fmt.Errorf("treekem: update identity %q does not match leaf identity %q", update.KeyPackage.Public.ID, rawNode.Leaf.ID)
	}

	rawNode.Leaf.EncryptionKey = append([]byte(nil), update.KeyPackage.Public.EncryptionKey...)
	rawNode.Leaf.SignatureKey = append([]byte(nil), update.KeyPackage.Public.SignatureKey...)
	rawNode.Leaf.ParentHash = nil
	rawNode.Leaf.Signature = append([]byte(nil), update.KeyPackage.Signature...)
	return nil
}
