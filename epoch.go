package netsec

import (
	"bytes"
	"crypto/hkdf"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"hash"
	"math"
	"sort"
)

const (
	// EpochSecretSize is the size of the secrets produced by the epoch key
	// schedule.
	EpochSecretSize = 32
	// CommitSecretSize is the size of the aggregated commit secret.
	CommitSecretSize = 32
)

const (
	groupContextDomain   = "netsec/q0200/mls/group-context/v1"
	commitSecretDomain   = "netsec/q0200/mls/commit-secret/v1"
	transcriptDomain     = "netsec/q0200/mls/confirmation-transcript/v1"
	commitIDDomain       = "netsec/q0200/mls/commit-id/v1"
	nextInitSecretLabel  = "netsec/q0200/mls/next-init-secret/v1"
	confirmationKeyLabel = "netsec/q0200/mls/confirmation-key/v1"
)

// Errors returned by the epoch schedule. Callers should use errors.Is rather
// than comparing error values directly.
var (
	// ErrInvalidCommit reports a malformed commit.
	ErrInvalidCommit = errors.New("netsec: invalid commit")
	// ErrInvalidState reports a malformed or incomplete epoch state.
	ErrInvalidState = errors.New("netsec: invalid epoch state")
	// ErrStaleEpoch reports a commit replayed from an epoch that has already
	// been advanced. This is what a removed member sees when it replays an
	// old commit with keys it retained.
	ErrStaleEpoch = errors.New("netsec: stale epoch commit")
	// ErrFutureEpoch reports a commit whose base epoch is newer than the
	// local state.
	ErrFutureEpoch = errors.New("netsec: commit from a future epoch")
	// ErrForkedCommit reports a commit built on a different parent tree,
	// transcript, or commit than the local epoch state.
	ErrForkedCommit = errors.New("netsec: forked commit")
	// ErrGroupMismatch reports a commit for a different group.
	ErrGroupMismatch = errors.New("netsec: group mismatch")
	// ErrUnknownLeaf reports a contribution from a leaf that is neither in
	// the current tree nor being removed by this commit.
	ErrUnknownLeaf = errors.New("netsec: contribution from unknown leaf")
	// ErrDuplicateLeaf reports duplicate leaf contributions.
	ErrDuplicateLeaf = errors.New("netsec: duplicate leaf contribution")
	// ErrMissingContribution reports an active leaf that did not contribute
	// to the commit secret.
	ErrMissingContribution = errors.New("netsec: missing leaf contribution")
	// ErrNoCommitSecret reports a commit with no active leaf contribution.
	ErrNoCommitSecret = errors.New("netsec: no active leaf contributes to commit secret")
)

// LeafID identifies a member leaf in the group ratchet tree.
type LeafID = uint32

// Member describes an active leaf in a group.
type Member struct {
	LeafID LeafID
	UserID []byte
}

// LeafContribution is the commit-secret material contributed by one leaf.
// Contributions are aggregated in leaf order, so their order in a Commit is
// irrelevant.
type LeafContribution struct {
	LeafID LeafID
	Secret []byte
}

// Commit describes an epoch transition. Epoch is the epoch the commit was
// built on, not the epoch it creates. A commit is applied only when it
// matches the local epoch.
//
// ParentTreeHash, ParentTranscriptHash, ParentCommitID, and
// ParentGroupContextHash are optional bindings to the state the committer
// built on. When they are present they must match the local state; a
// mismatch is rejected as a fork. Callers should set them for protocol
// messages received from peers.
type Commit struct {
	GroupID []byte
	Epoch   uint64

	ParentTreeHash         []byte
	ParentTranscriptHash   []byte
	ParentCommitID         []byte
	ParentGroupContextHash []byte

	TreeHash               []byte
	ConfirmationTranscript []byte

	RemovedLeaves []LeafID
	Contributions []LeafContribution

	// CommitSecret is an optional pre-aggregated commit secret for callers
	// whose Commit parser already performed the leaf aggregation. When set,
	// Contributions must be empty. The caller must have derived it with
	// DeriveCommitSecret, so removed leaves are already excluded.
	CommitSecret []byte
}

// EpochState is the local state needed to validate and apply a commit.
type EpochState struct {
	GroupID []byte
	Epoch   uint64

	// InitSecret is the epoch's init secret. It is never reused: processing
	// a commit replaces it with NextInitSecret derived from the new epoch
	// secret.
	InitSecret []byte

	TreeHash               []byte
	ConfirmationTranscript []byte
	TranscriptHash         []byte

	// Leaves contains the active leaves. It is updated to exclude leaves
	// removed by an accepted commit.
	Leaves []LeafID

	LastCommitID         []byte
	LastGroupContextHash []byte
}

// Clone returns a deep copy of the state.
func (s EpochState) Clone() EpochState {
	return EpochState{
		GroupID:                cloneBytes(s.GroupID),
		Epoch:                  s.Epoch,
		InitSecret:             cloneBytes(s.InitSecret),
		TreeHash:               cloneBytes(s.TreeHash),
		ConfirmationTranscript: cloneBytes(s.ConfirmationTranscript),
		TranscriptHash:         cloneBytes(s.TranscriptHash),
		Leaves:                 cloneLeaves(s.Leaves),
		LastCommitID:           cloneBytes(s.LastCommitID),
		LastGroupContextHash:   cloneBytes(s.LastGroupContextHash),
	}
}

// EpochAdvance is the result of a successful epoch transition. EpochSecret
// is the raw HKDF-Extract(init_secret, commit_secret) value from the MLS key
// schedule. BoundEpochSecret additionally mixes in the GroupContext hash and
// is used to derive the next init and confirmation keys.
type EpochAdvance struct {
	PreviousEpoch uint64
	Epoch         uint64

	CommitSecret       []byte
	ExtractedSecret    []byte
	EpochSecret        []byte
	BoundEpochSecret   []byte
	GroupContextHash   []byte
	ConfirmationKey    []byte
	EpochAuthenticator []byte

	NextInitSecret []byte
	InitSecret     []byte

	TreeHash               []byte
	ConfirmationTranscript []byte
	TranscriptHash         []byte

	CommitID      []byte
	RemovedLeaves []LeafID
	ActiveLeaves  []LeafID
}

// GroupContext is the context bound into the epoch key schedule. Its hash
// covers the new epoch, the tree hash, and the confirmation transcript.
type GroupContext struct {
	Epoch                  uint64
	TreeHash               []byte
	ConfirmationTranscript []byte
}

// Hash returns a domain-separated SHA-256 digest of the group context.
func (gc GroupContext) Hash() []byte {
	h := sha256.New()
	h.Write([]byte(groupContextDomain))
	writeUint64(h, gc.Epoch)
	writeField(h, gc.TreeHash)
	writeField(h, gc.ConfirmationTranscript)
	return h.Sum(nil)
}

// ComputeGroupContextHash returns SHA-256(domain || epoch || tree_hash ||
// confirmation_transcript), with length prefixes around the byte fields so
// the encoding is unambiguous.
func ComputeGroupContextHash(epoch uint64, treeHash, confirmationTranscript []byte) []byte {
	return GroupContext{
		Epoch:                  epoch,
		TreeHash:               treeHash,
		ConfirmationTranscript: confirmationTranscript,
	}.Hash()
}

// ComputeTranscriptHash hashes a confirmation transcript with domain
// separation. The raw transcript, not its hash, is bound by GroupContext.
func ComputeTranscriptHash(confirmationTranscript []byte) []byte {
	h := sha256.New()
	h.Write([]byte(transcriptDomain))
	writeField(h, confirmationTranscript)
	return h.Sum(nil)
}

// ComputeLeafTreeHash computes a deterministic service-level tree hash from
// the active leaf set. Protocols with a real ratchet-tree hash should pass
// that hash in Commit.TreeHash instead. Duplicate leaf IDs are ignored.
func ComputeLeafTreeHash(leaves []LeafID) []byte {
	unique := normalizeLeaves(leaves)
	h := sha256.New()
	h.Write([]byte("netsec/q0200/mls/leaf-tree-hash/v1"))
	writeUint32(h, uint32(len(unique)))
	for _, id := range unique {
		writeUint32(h, id)
	}
	return h.Sum(nil)
}

// ExtractEpochSecret performs the MLS HKDF-SHA256 extraction step:
//
//	HKDF-Extract(salt=commit_secret, ikm=init_secret)
func ExtractEpochSecret(initSecret, commitSecret []byte) ([]byte, error) {
	if len(initSecret) == 0 {
		return nil, fmt.Errorf("%w: empty init secret", ErrInvalidState)
	}
	if len(commitSecret) == 0 {
		return nil, fmt.Errorf("%w: empty commit secret", ErrInvalidCommit)
	}
	secret, err := hkdf.Extract(sha256.New, initSecret, commitSecret)
	if err != nil {
		return nil, fmt.Errorf("%w: HKDF extract: %v", ErrInvalidState, err)
	}
	return secret, nil
}

// DeriveEpochSecret derives the epoch secret from init_secret and
// commit_secret with HKDF-SHA256.
//
// With an optional group-context hash, the extracted secret is expanded with
// that hash as HKDF info. This binds the resulting epoch secret to the new
// epoch, tree hash, and confirmation transcript. Without a context hash the
// raw extraction output is returned.
func DeriveEpochSecret(initSecret, commitSecret []byte, contextHash ...[]byte) ([]byte, error) {
	extracted, err := ExtractEpochSecret(initSecret, commitSecret)
	if err != nil {
		return nil, err
	}
	if len(contextHash) == 0 || len(contextHash[0]) == 0 {
		return extracted, nil
	}
	secret, err := hkdf.Expand(sha256.New, extracted, string(contextHash[0]), EpochSecretSize)
	if err != nil {
		return nil, fmt.Errorf("%w: HKDF expand: %v", ErrInvalidState, err)
	}
	return secret, nil
}

// DeriveCommitSecret aggregates per-leaf contributions into the commit
// secret. Contributions from leaves listed in removed are ignored, so a
// member that has been removed cannot influence the next epoch's commit
// secret. Contributions are sorted by leaf ID to make the result
// deterministic and independent of message ordering.
func DeriveCommitSecret(contributions []LeafContribution, removed []LeafID) ([]byte, error) {
	removedSet := make(map[LeafID]struct{}, len(removed))
	for _, id := range removed {
		if _, duplicate := removedSet[id]; duplicate {
			return nil, fmt.Errorf("%w: leaf %d removed more than once", ErrInvalidCommit, id)
		}
		removedSet[id] = struct{}{}
	}

	seen := make(map[LeafID]struct{}, len(contributions))
	active := make([]LeafContribution, 0, len(contributions))
	for _, contribution := range contributions {
		if _, duplicate := seen[contribution.LeafID]; duplicate {
			return nil, fmt.Errorf("%w: leaf %d", ErrDuplicateLeaf, contribution.LeafID)
		}
		seen[contribution.LeafID] = struct{}{}
		if len(contribution.Secret) == 0 {
			return nil, fmt.Errorf("%w: leaf %d has an empty secret", ErrInvalidCommit, contribution.LeafID)
		}
		if _, isRemoved := removedSet[contribution.LeafID]; isRemoved {
			continue
		}
		active = append(active, LeafContribution{
			LeafID: contribution.LeafID,
			Secret: cloneBytes(contribution.Secret),
		})
	}
	if len(active) == 0 {
		return nil, ErrNoCommitSecret
	}

	sort.Slice(active, func(i, j int) bool {
		return active[i].LeafID < active[j].LeafID
	})

	h := sha256.New()
	h.Write([]byte(commitSecretDomain))
	writeUint32(h, uint32(len(active)))
	for _, contribution := range active {
		writeUint32(h, contribution.LeafID)
		writeField(h, contribution.Secret)
	}
	return h.Sum(nil), nil
}

// AggregateCommitSecret is DeriveCommitSecret without any removed leaves.
func AggregateCommitSecret(contributions []LeafContribution) ([]byte, error) {
	return DeriveCommitSecret(contributions, nil)
}

// AdvanceEpoch validates commit against state and returns the next epoch
// state and the derived epoch material. The input state is not modified.
//
// A commit whose base epoch is lower than the local epoch is rejected as a
// stale replay, and a commit built on a different parent is rejected as a
// fork. The transition is all-or-nothing.
func AdvanceEpoch(state EpochState, commit Commit) (EpochState, EpochAdvance, error) {
	return computeAdvance(state, commit)
}

// ProcessCommit validates commit against state and, on success, atomically
// replaces state with the next epoch. On failure state is unchanged.
//
// This is the entry point a group message service should call while
// processing a Commit message.
func ProcessCommit(state *EpochState, commit Commit) (*EpochAdvance, error) {
	if state == nil {
		return nil, fmt.Errorf("%w: nil state", ErrInvalidState)
	}
	snapshot := *state
	next, advance, err := computeAdvance(snapshot, commit)
	if err != nil {
		return nil, err
	}
	*state = next
	return &advance, nil
}

// ApplyCommit is ProcessCommit without returning the derived key material.
func ApplyCommit(state *EpochState, commit Commit) error {
	_, err := ProcessCommit(state, commit)
	return err
}

func computeAdvance(state EpochState, commit Commit) (EpochState, EpochAdvance, error) {
	if len(state.InitSecret) == 0 {
		return EpochState{}, EpochAdvance{}, fmt.Errorf("%w: empty init secret", ErrInvalidState)
	}
	if state.Epoch == math.MaxUint64 {
		return EpochState{}, EpochAdvance{}, fmt.Errorf("%w: epoch overflow", ErrInvalidState)
	}
	if len(commit.GroupID) > 0 {
		if len(state.GroupID) == 0 || !bytes.Equal(state.GroupID, commit.GroupID) {
			return EpochState{}, EpochAdvance{}, fmt.Errorf("%w: commit group does not match state", ErrGroupMismatch)
		}
	}

	switch {
	case commit.Epoch < state.Epoch:
		return EpochState{}, EpochAdvance{}, fmt.Errorf(
			"%w: commit epoch %d, local epoch %d", ErrStaleEpoch, commit.Epoch, state.Epoch,
		)
	case commit.Epoch > state.Epoch:
		return EpochState{}, EpochAdvance{}, fmt.Errorf(
			"%w: commit epoch %d, local epoch %d", ErrFutureEpoch, commit.Epoch, state.Epoch,
		)
	}

	if len(commit.ParentTreeHash) > 0 && !bytes.Equal(commit.ParentTreeHash, state.TreeHash) {
		return EpochState{}, EpochAdvance{}, fmt.Errorf(
			"%w: parent tree hash differs from local state", ErrForkedCommit,
		)
	}
	parentTranscriptHash := state.TranscriptHash
	if len(parentTranscriptHash) == 0 && len(state.ConfirmationTranscript) > 0 {
		parentTranscriptHash = ComputeTranscriptHash(state.ConfirmationTranscript)
	}
	if len(commit.ParentTranscriptHash) > 0 && !bytes.Equal(commit.ParentTranscriptHash, parentTranscriptHash) {
		return EpochState{}, EpochAdvance{}, fmt.Errorf(
			"%w: parent transcript hash differs from local state", ErrForkedCommit,
		)
	}
	if len(commit.ParentCommitID) > 0 {
		if len(state.LastCommitID) == 0 || !bytes.Equal(commit.ParentCommitID, state.LastCommitID) {
			return EpochState{}, EpochAdvance{}, fmt.Errorf(
				"%w: parent commit differs from local state", ErrForkedCommit,
			)
		}
	}
	if len(commit.ParentGroupContextHash) > 0 {
		parentContextHash := state.LastGroupContextHash
		if len(parentContextHash) == 0 {
			parentContextHash = ComputeGroupContextHash(
				state.Epoch, state.TreeHash, state.ConfirmationTranscript,
			)
		}
		if !bytes.Equal(commit.ParentGroupContextHash, parentContextHash) {
			return EpochState{}, EpochAdvance{}, fmt.Errorf(
				"%w: parent group context differs from local state", ErrForkedCommit,
			)
		}
	}

	stateLeaves, err := validateLeaves(state.Leaves)
	if err != nil {
		return EpochState{}, EpochAdvance{}, err
	}
	removedSet, removedLeaves, err := validateRemoved(commit.RemovedLeaves, stateLeaves)
	if err != nil {
		return EpochState{}, EpochAdvance{}, err
	}

	var activeSet map[LeafID]struct{}
	if len(stateLeaves) > 0 {
		activeSet = make(map[LeafID]struct{}, len(stateLeaves)-len(removedLeaves))
		for _, id := range stateLeaves {
			if _, removed := removedSet[id]; removed {
				continue
			}
			activeSet[id] = struct{}{}
		}
	}

	directCommitSecret := len(commit.CommitSecret) > 0
	if directCommitSecret && len(commit.Contributions) > 0 {
		return EpochState{}, EpochAdvance{}, fmt.Errorf(
			"%w: both CommitSecret and per-leaf contributions are set", ErrInvalidCommit,
		)
	}

	var commitSecret []byte
	var activeLeaves []LeafID
	if directCommitSecret {
		if activeSet == nil {
			return EpochState{}, EpochAdvance{}, fmt.Errorf(
				"%w: CommitSecret requires the active leaf set", ErrInvalidState,
			)
		}
		activeLeaves = make([]LeafID, 0, len(activeSet))
		for id := range activeSet {
			activeLeaves = append(activeLeaves, id)
		}
		sort.Slice(activeLeaves, func(i, j int) bool { return activeLeaves[i] < activeLeaves[j] })
		if len(activeLeaves) == 0 {
			return EpochState{}, EpochAdvance{}, fmt.Errorf(
				"%w: all leaves were removed", ErrNoCommitSecret,
			)
		}
		commitSecret = cloneBytes(commit.CommitSecret)
	} else {
		secrets := make(map[LeafID][]byte, len(commit.Contributions))
		seenContributions := make(map[LeafID]struct{}, len(commit.Contributions))
		for _, contribution := range commit.Contributions {
			if _, duplicate := seenContributions[contribution.LeafID]; duplicate {
				return EpochState{}, EpochAdvance{}, fmt.Errorf(
					"%w: leaf %d", ErrDuplicateLeaf, contribution.LeafID,
				)
			}
			seenContributions[contribution.LeafID] = struct{}{}
			if len(contribution.Secret) == 0 {
				return EpochState{}, EpochAdvance{}, fmt.Errorf(
					"%w: leaf %d has an empty secret", ErrInvalidCommit, contribution.LeafID,
				)
			}
			if _, removed := removedSet[contribution.LeafID]; removed {
				// A removed member may still send a contribution using keys
				// it retained from the old epoch. It must not affect the
				// result.
				continue
			}
			if activeSet != nil {
				if _, ok := activeSet[contribution.LeafID]; !ok {
					return EpochState{}, EpochAdvance{}, fmt.Errorf(
						"%w: leaf %d", ErrUnknownLeaf, contribution.LeafID,
					)
				}
			}
			secrets[contribution.LeafID] = cloneBytes(contribution.Secret)
		}

		if activeSet == nil {
			activeSet = make(map[LeafID]struct{}, len(secrets))
			for id := range secrets {
				activeSet[id] = struct{}{}
			}
		}
		if len(activeSet) == 0 {
			return EpochState{}, EpochAdvance{}, fmt.Errorf(
				"%w: all leaves were removed", ErrNoCommitSecret,
			)
		}

		activeLeaves = make([]LeafID, 0, len(activeSet))
		activeContributions := make([]LeafContribution, 0, len(activeSet))
		for id := range activeSet {
			secret, ok := secrets[id]
			if !ok {
				return EpochState{}, EpochAdvance{}, fmt.Errorf(
					"%w: leaf %d", ErrMissingContribution, id,
				)
			}
			activeLeaves = append(activeLeaves, id)
			activeContributions = append(activeContributions, LeafContribution{
				LeafID: id,
				Secret: secret,
			})
		}
		sort.Slice(activeLeaves, func(i, j int) bool { return activeLeaves[i] < activeLeaves[j] })

		var err error
		commitSecret, err = DeriveCommitSecret(activeContributions, nil)
		if err != nil {
			return EpochState{}, EpochAdvance{}, err
		}
	}

	newEpoch := state.Epoch + 1
	newTreeHash := cloneBytes(commit.TreeHash)
	if len(newTreeHash) == 0 {
		newTreeHash = ComputeLeafTreeHash(activeLeaves)
	}
	transcript := cloneBytes(commit.ConfirmationTranscript)
	context := GroupContext{
		Epoch:                  newEpoch,
		TreeHash:               newTreeHash,
		ConfirmationTranscript: transcript,
	}
	contextHash := context.Hash()

	extractedSecret, err := ExtractEpochSecret(state.InitSecret, commitSecret)
	if err != nil {
		return EpochState{}, EpochAdvance{}, err
	}
	epochSecret := extractedSecret
	boundEpochSecret, err := hkdf.Expand(sha256.New, epochSecret, string(contextHash), EpochSecretSize)
	if err != nil {
		return EpochState{}, EpochAdvance{}, fmt.Errorf("%w: bind epoch secret: %v", ErrInvalidState, err)
	}
	nextInitSecret, err := hkdf.Expand(
		sha256.New, boundEpochSecret, nextInitSecretLabel+string(contextHash), EpochSecretSize,
	)
	if err != nil {
		return EpochState{}, EpochAdvance{}, fmt.Errorf("%w: derive next init secret: %v", ErrInvalidState, err)
	}
	confirmationKey, err := hkdf.Expand(
		sha256.New, boundEpochSecret, confirmationKeyLabel+string(contextHash), EpochSecretSize,
	)
	if err != nil {
		return EpochState{}, EpochAdvance{}, fmt.Errorf("%w: derive confirmation key: %v", ErrInvalidState, err)
	}
	authenticator := hmac.New(sha256.New, confirmationKey)
	authenticator.Write(transcript)
	epochAuthenticator := authenticator.Sum(nil)

	transcriptHash := ComputeTranscriptHash(transcript)
	commitID := commit.ID()

	nextState := EpochState{
		GroupID:                cloneBytes(state.GroupID),
		Epoch:                  newEpoch,
		InitSecret:             cloneBytes(nextInitSecret),
		TreeHash:               cloneBytes(newTreeHash),
		ConfirmationTranscript: cloneBytes(transcript),
		TranscriptHash:         cloneBytes(transcriptHash),
		Leaves:                 cloneLeaves(activeLeaves),
		LastCommitID:           cloneBytes(commitID),
		LastGroupContextHash:   cloneBytes(contextHash),
	}
	advance := EpochAdvance{
		PreviousEpoch:          state.Epoch,
		Epoch:                  newEpoch,
		CommitSecret:           cloneBytes(commitSecret),
		ExtractedSecret:        cloneBytes(extractedSecret),
		EpochSecret:            cloneBytes(epochSecret),
		BoundEpochSecret:       cloneBytes(boundEpochSecret),
		GroupContextHash:       cloneBytes(contextHash),
		ConfirmationKey:        cloneBytes(confirmationKey),
		EpochAuthenticator:     cloneBytes(epochAuthenticator),
		NextInitSecret:         cloneBytes(nextInitSecret),
		InitSecret:             cloneBytes(nextInitSecret),
		TreeHash:               cloneBytes(newTreeHash),
		ConfirmationTranscript: cloneBytes(transcript),
		TranscriptHash:         cloneBytes(transcriptHash),
		CommitID:               cloneBytes(commitID),
		RemovedLeaves:          cloneLeaves(removedLeaves),
		ActiveLeaves:           cloneLeaves(activeLeaves),
	}
	return nextState, advance, nil
}

// ID returns a deterministic identifier for a commit. Contributions and
// removed leaves are sorted, so their ordering does not affect the ID.
func (c Commit) ID() []byte {
	h := sha256.New()
	h.Write([]byte(commitIDDomain))
	writeField(h, c.GroupID)
	writeUint64(h, c.Epoch)
	writeField(h, c.ParentTreeHash)
	writeField(h, c.ParentTranscriptHash)
	writeField(h, c.ParentCommitID)
	writeField(h, c.ParentGroupContextHash)
	writeField(h, c.TreeHash)
	writeField(h, c.ConfirmationTranscript)

	removed := normalizeLeaves(c.RemovedLeaves)
	writeUint32(h, uint32(len(removed)))
	for _, id := range removed {
		writeUint32(h, id)
	}

	contributions := make([]LeafContribution, 0, len(c.Contributions))
	for _, contribution := range c.Contributions {
		contributions = append(contributions, LeafContribution{
			LeafID: contribution.LeafID,
			Secret: cloneBytes(contribution.Secret),
		})
	}
	sort.Slice(contributions, func(i, j int) bool {
		return contributions[i].LeafID < contributions[j].LeafID
	})
	writeUint32(h, uint32(len(contributions)))
	for _, contribution := range contributions {
		writeUint32(h, contribution.LeafID)
		writeField(h, contribution.Secret)
	}
	writeField(h, c.CommitSecret)
	return h.Sum(nil)
}

func validateLeaves(leaves []LeafID) ([]LeafID, error) {
	normalized := normalizeLeaves(leaves)
	if len(normalized) != len(leaves) {
		return nil, fmt.Errorf("%w: duplicate leaf in state", ErrInvalidState)
	}
	return normalized, nil
}

func validateRemoved(removed, stateLeaves []LeafID) (map[LeafID]struct{}, []LeafID, error) {
	removedSet := make(map[LeafID]struct{}, len(removed))
	for _, id := range removed {
		if _, duplicate := removedSet[id]; duplicate {
			return nil, nil, fmt.Errorf("%w: leaf %d removed more than once", ErrInvalidCommit, id)
		}
		removedSet[id] = struct{}{}
	}
	if len(stateLeaves) == 0 {
		return removedSet, normalizeLeaves(removed), nil
	}
	active := make(map[LeafID]struct{}, len(stateLeaves))
	for _, id := range stateLeaves {
		active[id] = struct{}{}
	}
	for id := range removedSet {
		if _, ok := active[id]; !ok {
			return nil, nil, fmt.Errorf("%w: leaf %d is not an active member", ErrInvalidCommit, id)
		}
	}
	return removedSet, normalizeLeaves(removed), nil
}

func normalizeLeaves(leaves []LeafID) []LeafID {
	unique := make(map[LeafID]struct{}, len(leaves))
	for _, id := range leaves {
		unique[id] = struct{}{}
	}
	normalized := make([]LeafID, 0, len(unique))
	for id := range unique {
		normalized = append(normalized, id)
	}
	sort.Slice(normalized, func(i, j int) bool { return normalized[i] < normalized[j] })
	return normalized
}

func cloneLeaves(leaves []LeafID) []LeafID {
	if leaves == nil {
		return nil
	}
	return append([]LeafID(nil), leaves...)
}

func cloneBytes(data []byte) []byte {
	if data == nil {
		return nil
	}
	return append([]byte(nil), data...)
}

func writeUint64(h hash.Hash, value uint64) {
	var buf [8]byte
	binary.BigEndian.PutUint64(buf[:], value)
	h.Write(buf[:])
}

func writeUint32(h hash.Hash, value uint32) {
	var buf [4]byte
	binary.BigEndian.PutUint32(buf[:], value)
	h.Write(buf[:])
}

func writeField(h hash.Hash, data []byte) {
	writeUint64(h, uint64(len(data)))
	h.Write(data)
}
