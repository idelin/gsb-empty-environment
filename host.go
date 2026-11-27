package treekem

import (
	"crypto/ecdh"
	"errors"
	"fmt"
)

// ErrRemovedFromGroup is returned when a removed host tries to accept a commit.
var ErrRemovedFromGroup = errors.New("treekem: host was removed by commit")

// Host is one member's private group state. It is safe to use as a local
// protocol engine; synchronization is the caller's responsibility.
type Host struct {
	GroupID      []byte
	Identity     *Identity
	LeafPosition int
	Tree         *RatchetTree
	Epoch        uint64
	Transcript   *Transcript
	InitSecret   []byte
	Secrets      *EpochSecrets
	Ring         map[Index]*ecdh.PrivateKey
	Pending      []Proposal

	pendingPathIdentity *Identity
}

// Group is the conventional state-machine alias for a local member Host.
type Group = Host

// Welcome is the encrypted joiner_secret plus the finalized public tree needed
// by a newly added member in this self-contained model.
type Welcome struct {
	GroupID               []byte
	Epoch                 uint64
	AddedLeafPosition     int
	GroupContext          *GroupContext
	InterimTranscriptHash []byte
	Tree                  *RatchetTree
	JoinerEnvelope        *Envelope
}

// NewGroup creates epoch zero with a freshly generated creator identity.
func NewGroup(groupID []byte, creatorID string) (*Host, error) {
	identity, err := NewIdentity(creatorID)
	if err != nil {
		return nil, err
	}
	return NewHostWithIdentity(groupID, identity)
}

// NewHostWithIdentity creates epoch zero using a caller-supplied identity.
func NewHostWithIdentity(groupID []byte, identity *Identity) (*Host, error) {
	if len(groupID) == 0 {
		return nil, errors.New("treekem: group ID is empty")
	}
	if identity == nil {
		return nil, errors.New("treekem: nil identity")
	}
	public := identity.Public()
	tree, err := NewSingleLeafTree(public)
	if err != nil {
		return nil, err
	}
	leaf := tree.Nodes[0].Leaf
	if err := signLeaf(identity, leaf); err != nil {
		return nil, err
	}
	transcript := NewInitialTranscript(groupID)
	context := &GroupContext{
		GroupID:                 append([]byte(nil), groupID...),
		Epoch:                   0,
		TreeHash:                tree.Hash(),
		ConfirmedTranscriptHash: transcript.Confirmed,
	}
	joiner, err := randomSecret()
	if err != nil {
		return nil, err
	}
	secrets, err := DeriveEpochSecrets(joiner, context)
	if err != nil {
		return nil, err
	}
	return &Host{
		GroupID:      append([]byte(nil), groupID...),
		Identity:     identity,
		LeafPosition: 0,
		Tree:         tree,
		Epoch:        0,
		Transcript:   transcript,
		InitSecret:   append([]byte(nil), secrets.InitSecret...),
		Secrets:      secrets,
		Ring:         map[Index]*ecdh.PrivateKey{},
	}, nil
}

// QueueAdd validates and queues an Add proposal.
func (h *Host) QueueAdd(keyPackage *KeyPackage) error {
	proposal, err := NewAddProposal(keyPackage)
	if err != nil {
		return err
	}
	h.Pending = append(h.Pending, proposal)
	return nil
}

// QueueRemove queues a Remove proposal.
func (h *Host) QueueRemove(leafPosition int) {
	h.Pending = append(h.Pending, NewRemoveProposal(leafPosition))
}

// QueueUpdate queues an already-signed Update proposal.
func (h *Host) QueueUpdate(proposal Proposal) error {
	if proposal.Kind != ProposalUpdate {
		return fmt.Errorf("treekem: expected update proposal, got %q", proposal.Kind)
	}
	if proposal.Update != nil && proposal.Update.LeafPosition == h.LeafPosition {
		return errors.New("treekem: use RotateIdentityAndQueueUpdate for self-updates so the private key is available")
	}
	h.Pending = append(h.Pending, proposal)
	return nil
}

// RotateIdentityAndQueueUpdate generates a new same-ID identity and queues the
// self Update proposal. The new private identity is installed only after Commit.
func (h *Host) RotateIdentityAndQueueUpdate() (*Identity, error) {
	newIdentity, err := NewIdentity(h.Identity.public.ID)
	if err != nil {
		return nil, err
	}
	keyPackage, err := newIdentity.KeyPackage()
	if err != nil {
		return nil, err
	}
	proposal, err := NewUpdateProposal(h.LeafPosition, keyPackage)
	if err != nil {
		return nil, err
	}
	h.Pending = append(h.Pending, proposal)
	h.pendingPathIdentity = newIdentity
	return newIdentity, nil
}

func welcomeAAD(groupID []byte, epoch uint64, addedPosition int) []byte {
	hb := &hashBuilder{}
	hb.bytes([]byte("treekem-welcome-aad-v1"))
	hb.bytes(groupID)
	hb.uint64(epoch)
	hb.uint32(uint32(addedPosition))
	return hb.buf
}

// Commit builds, signs, applies, and returns the queued proposal batch plus an
// encrypted Welcome for every Add proposal.
func (h *Host) Commit() (*Commit, []*Welcome, error) {
	if h.pendingPathIdentity != nil && !pendingContainsSelfUpdate(h.Pending, h.LeafPosition) {
		return nil, nil, errors.New("treekem: pending rotated identity has no self update")
	}

	workTree := h.Tree.Clone()
	applied, err := ApplyProposals(workTree, h.Pending)
	if err != nil {
		return nil, nil, err
	}
	for _, removed := range applied.RemovedLeafPositions {
		if removed == h.LeafPosition {
			return nil, nil, errors.New("treekem: committer cannot remove itself in this state machine")
		}
	}

	newEpoch := h.Epoch + 1
	pathSigner := h.Identity
	if h.pendingPathIdentity != nil {
		pathSigner = h.pendingPathIdentity
	}
	built, err := buildUpdatePath(workTree, pathSigner, h.LeafPosition, h.GroupID, newEpoch)
	if err != nil {
		return nil, nil, err
	}
	if err := installUpdatePath(workTree, built.Path); err != nil {
		return nil, nil, err
	}
	if err := VerifyCommittedTree(workTree, h.LeafPosition); err != nil {
		return nil, nil, err
	}

	commit := &Commit{
		GroupID:   append([]byte(nil), h.GroupID...),
		Epoch:     newEpoch,
		Committer: h.LeafPosition,
		Proposals: append([]Proposal(nil), h.Pending...),
		Path:      built.Path,
	}
	transcriptInput := *commit
	transcriptInput.Confirmation = nil
	confirmedHash := hashBytes("treekem-confirmed-transcript-v1", h.Transcript.Confirmed, encodeCommit(&transcriptInput))
	context := &GroupContext{
		GroupID:                 append([]byte(nil), h.GroupID...),
		Epoch:                   newEpoch,
		TreeHash:                workTree.Hash(),
		ConfirmedTranscriptHash: confirmedHash,
	}
	joinerSecret := JoinerSecret(h.InitSecret, built.CommitSecret)
	secrets, err := DeriveEpochSecrets(joinerSecret, context)
	if err != nil {
		return nil, nil, err
	}
	commit.Confirmation = confirmationTag(secrets.ConfirmationKey, confirmedHash)
	if err := signCommit(h.Identity, commit); err != nil {
		return nil, nil, err
	}

	addKeyPackages := addProposalsInOrder(h.Pending)
	if len(addKeyPackages) != len(applied.AddedLeafPositions) {
		return nil, nil, errors.New("treekem: add proposal count mismatch")
	}
	welcomes := make([]*Welcome, 0, len(addKeyPackages))
	for i, keyPackage := range addKeyPackages {
		position := applied.AddedLeafPositions[i]
		aad := welcomeAAD(h.GroupID, newEpoch, position)
		envelope, err := sealEnvelope(keyPackage.Public.EncryptionKey, aad, joinerSecret)
		if err != nil {
			return nil, nil, err
		}
		newInterim := hashBytes("treekem-interim-transcript-v1", h.Transcript.Interim, commit.Confirmation)
		welcomes = append(welcomes, &Welcome{
			GroupID:               append([]byte(nil), h.GroupID...),
			Epoch:                 newEpoch,
			AddedLeafPosition:     position,
			GroupContext:          context,
			InterimTranscriptHash: newInterim,
			Tree:                  workTree.Clone(),
			JoinerEnvelope:        envelope,
		})
	}

	inputBytes := encodeCommit(&transcriptInput)
	h.Tree = workTree
	h.Epoch = newEpoch
	h.Transcript = h.Transcript.advance(inputBytes, commit.Confirmation)
	h.InitSecret = append([]byte(nil), secrets.InitSecret...)
	h.Secrets = secrets
	if h.Ring == nil {
		h.Ring = map[Index]*ecdh.PrivateKey{}
	}
	for index, privateKey := range built.PrivateKeys {
		h.Ring[index] = privateKey
	}
	if h.pendingPathIdentity != nil {
		h.Identity = h.pendingPathIdentity
	}
	h.Pending = nil
	h.pendingPathIdentity = nil
	return commit, welcomes, nil
}

// AcceptCommit independently validates and applies a Commit to an existing member.
func (h *Host) AcceptCommit(commit *Commit) (*AppliedProposals, error) {
	if commit == nil {
		return nil, errors.New("treekem: nil commit")
	}
	if string(commit.GroupID) != string(h.GroupID) {
		return nil, fmt.Errorf("treekem: group ID mismatch")
	}
	if commit.Epoch != h.Epoch+1 {
		return nil, fmt.Errorf("treekem: expected epoch %d, got %d", h.Epoch+1, commit.Epoch)
	}
	if err := VerifyCommitSignature(h.Tree, commit); err != nil {
		return nil, err
	}

	for _, proposal := range commit.Proposals {
		if proposal.Kind == ProposalRemove && proposal.RemoveLeafPosition == h.LeafPosition {
			return nil, fmt.Errorf("%w: leaf %d", ErrRemovedFromGroup, h.LeafPosition)
		}
	}

	workTree := h.Tree.Clone()
	applied, err := ApplyProposals(workTree, commit.Proposals)
	if err != nil {
		return nil, err
	}
	if commit.Committer < 0 || commit.Committer >= workTree.LeafCount {
		return nil, fmt.Errorf("%w: committer %d", ErrInvalidIndex, commit.Committer)
	}
	committerLeaf := workTree.Nodes[Leaf(commit.Committer)]
	if committerLeaf.Blank() {
		return nil, errors.New("treekem: committer leaf is blank after proposals")
	}

	decrypted, err := TryDecryptPathForLeaf(workTree, commit, h.LeafPosition, h.Identity.encPriv, h.Ring)
	if err != nil {
		return nil, fmt.Errorf("decrypt update path: %w", err)
	}
	if err := installUpdatePath(workTree, commit.Path); err != nil {
		return nil, err
	}
	if err := VerifyCommittedTree(workTree, commit.Committer); err != nil {
		return nil, err
	}

	transcriptInput := *commit
	transcriptInput.Confirmation = nil
	inputBytes := encodeCommit(&transcriptInput)
	confirmedHash := hashBytes("treekem-confirmed-transcript-v1", h.Transcript.Confirmed, inputBytes)
	context := &GroupContext{
		GroupID:                 append([]byte(nil), h.GroupID...),
		Epoch:                   commit.Epoch,
		TreeHash:                workTree.Hash(),
		ConfirmedTranscriptHash: confirmedHash,
	}
	joinerSecret := JoinerSecret(h.InitSecret, decrypted.CommitSecret)
	secrets, err := DeriveEpochSecrets(joinerSecret, context)
	if err != nil {
		return nil, err
	}
	if !verifyConfirmation(secrets.ConfirmationKey, confirmedHash, commit.Confirmation) {
		return nil, errors.New("treekem: commit confirmation tag mismatch")
	}

	h.Tree = workTree
	h.Epoch = commit.Epoch
	h.Transcript = h.Transcript.advance(inputBytes, commit.Confirmation)
	h.InitSecret = append([]byte(nil), secrets.InitSecret...)
	h.Secrets = secrets
	if h.Ring == nil {
		h.Ring = map[Index]*ecdh.PrivateKey{}
	}
	for index, privateKey := range decrypted.PrivateKeys {
		h.Ring[index] = privateKey
	}
	h.Pending = nil
	h.pendingPathIdentity = nil
	return applied, nil
}

// JoinFromWelcome creates the new member's state and populates its path key ring.
func JoinFromWelcome(identity *Identity, welcome *Welcome, commit *Commit) (*Host, error) {
	if identity == nil || welcome == nil || commit == nil {
		return nil, errors.New("treekem: identity, welcome, and commit are required")
	}
	if welcome.GroupContext == nil {
		return nil, errors.New("treekem: welcome missing group context")
	}
	aad := welcomeAAD(welcome.GroupID, welcome.Epoch, welcome.AddedLeafPosition)
	joinerSecret, err := openEnvelope(identity.encPriv, aad, welcome.JoinerEnvelope)
	if err != nil {
		return nil, fmt.Errorf("open welcome: %w", err)
	}
	tree := welcome.Tree.Clone()
	if err := VerifyCommittedTree(tree, commit.Committer); err != nil {
		return nil, err
	}
	if !constantTimeEqual(tree.Hash(), welcome.GroupContext.TreeHash) {
		return nil, errors.New("treekem: welcome tree hash mismatch")
	}
	leafIndex := Leaf(welcome.AddedLeafPosition)
	rawLeaf, err := tree.node(leafIndex)
	if err != nil {
		return nil, err
	}
	if rawLeaf.Blank() || rawLeaf.Leaf == nil {
		return nil, errors.New("treekem: welcome leaf is blank")
	}
	if rawLeaf.Leaf.ID != identity.public.ID ||
		!bytesEqual(rawLeaf.Leaf.EncryptionKey, identity.public.EncryptionKey) {
		return nil, errors.New("treekem: welcome leaf does not match identity")
	}
	secrets, err := DeriveEpochSecrets(joinerSecret, welcome.GroupContext)
	if err != nil {
		return nil, err
	}
	if !verifyConfirmation(secrets.ConfirmationKey, welcome.GroupContext.ConfirmedTranscriptHash, commit.Confirmation) {
		return nil, errors.New("treekem: welcome commit confirmation mismatch")
	}

	host := &Host{
		GroupID:      append([]byte(nil), welcome.GroupID...),
		Identity:     identity,
		LeafPosition: welcome.AddedLeafPosition,
		Tree:         tree,
		Epoch:        welcome.Epoch,
		Transcript: &Transcript{
			Confirmed: append([]byte(nil), welcome.GroupContext.ConfirmedTranscriptHash...),
			Interim:   append([]byte(nil), welcome.InterimTranscriptHash...),
		},
		InitSecret: append([]byte(nil), secrets.InitSecret...),
		Secrets:    secrets,
		Ring:       map[Index]*ecdh.PrivateKey{},
	}
	decrypted, err := TryDecryptPathForLeaf(tree, commit, welcome.AddedLeafPosition, identity.encPriv, host.Ring)
	if err != nil {
		return nil, fmt.Errorf("new member path decrypt: %w", err)
	}
	for index, privateKey := range decrypted.PrivateKeys {
		host.Ring[index] = privateKey
	}
	return host, nil
}

func addProposalsInOrder(proposals []Proposal) []*KeyPackage {
	out := []*KeyPackage{}
	for _, proposal := range proposals {
		if proposal.Kind == ProposalAdd {
			out = append(out, proposal.Add)
		}
	}
	return out
}

func pendingContainsSelfUpdate(proposals []Proposal, position int) bool {
	for _, proposal := range proposals {
		if proposal.Kind == ProposalUpdate && proposal.Update != nil && proposal.Update.LeafPosition == position {
			return true
		}
	}
	return false
}

func bytesEqual(a, b []byte) bool {
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
