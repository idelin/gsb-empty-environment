package netsec

import (
	"bytes"
	"crypto/ed25519"
	"math"
	"sync"
)

// SuccessCallback is invoked by Open only after a message has authenticated
// against a chain installed for the message epoch. The sender argument is
// guaranteed to belong to the signed member set of that epoch; the state
// machine enforces this before any callback can run.
type SuccessCallback func(sender DeviceID, header Header, plaintext []byte) error

type senderChain struct {
	chainID   ChainID
	handle    Handle
	nextIndex uint64
}

type skippedKey struct {
	sender  DeviceID
	chainID ChainID
	index   uint64
	key     []byte
}

type epochInfo struct {
	members MemberSet
	digest  Digest
}

// GroupState holds the complete sender-key state for one group on one device.
// All methods are safe for concurrent use; a single mutex serializes epoch
// transitions against sealing and opening so epochs cannot be applied
// concurrently with chain operations.
type GroupState struct {
	mu sync.Mutex

	groupID []byte
	admin   ed25519.PublicKey
	store   ChainKeyStore

	epoch   uint64
	members MemberSet
	digest  Digest

	// chains holds exactly one current chain per sender device.
	chains map[DeviceID]*senderChain
	// usedChainIDs is the global, cross-epoch registry of every chainID ever
	// installed in this group.
	usedChainIDs map[ChainID]struct{}
	// skipped caches at most MaxSkippedKeys one-time message keys.
	skipped []skippedKey
	// history retains the signed member set of every epoch observed, so a
	// successful callback can always be checked against its message epoch.
	history map[uint64]epochInfo
}

// NewGroupState creates state for groupID starting at epoch zero with the
// given initial member set and administrator Ed25519 public key. No sender
// chains are installed until signed distributions arrive.
func NewGroupState(groupID []byte, admin ed25519.PublicKey, members MemberSet, store ChainKeyStore) *GroupState {
	initial := members.Clone()
	digest := initial.Digest()

	state := &GroupState{
		groupID:      append([]byte(nil), groupID...),
		admin:        append(ed25519.PublicKey(nil), admin...),
		store:        store,
		epoch:        0,
		members:      initial,
		digest:       digest,
		chains:       make(map[DeviceID]*senderChain),
		usedChainIDs: make(map[ChainID]struct{}),
		history: map[uint64]epochInfo{
			0: {members: initial, digest: digest},
		},
	}
	return state
}

// GroupID returns the group identifier.
func (s *GroupState) GroupID() []byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]byte(nil), s.groupID...)
}

// Epoch returns the current, strictly increasing group epoch.
func (s *GroupState) Epoch() uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.epoch
}

// Members returns a copy of the current member device set.
func (s *GroupState) Members() MemberSet {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.members.Clone()
}

// MembersDigest returns the canonical digest of the current member set.
func (s *GroupState) MembersDigest() Digest {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.digest
}

// ChainStatus reports the installed chain state for a sender device. The
// boolean is false when no chain is installed.
func (s *GroupState) ChainStatus(sender DeviceID) (chainID ChainID, handle Handle, nextIndex uint64, ok bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	chain, found := s.chains[sender]
	if !found {
		return ChainID{}, "", 0, false
	}
	return chain.chainID, chain.handle, chain.nextIndex, true
}

// ApplyDistribution validates and installs a signed chain distribution. It
// enforces that the signature is valid, the epoch is current, the signed
// member-set digest matches the current members, the sender is a member, and
// the chainID has never been used in any epoch of the group.
func (s *GroupState) ApplyDistribution(d *Distribution, initialChainKey []byte, handle Handle) error {
	if !bytes.Equal(d.GroupID, s.groupID) {
		return ErrGroupID
	}
	if len(initialChainKey) != 32 {
		return ErrInvalidChainKey
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if d.Epoch != s.epoch {
		return ErrEpoch
	}
	if !VerifyDistribution(d, s.admin) {
		return ErrSignature
	}
	if d.MembersDigest != s.digest {
		return ErrDigestMismatch
	}
	if !s.members.Contains(d.SenderDevice) {
		return ErrNotMember
	}
	if _, reused := s.usedChainIDs[d.ChainID]; reused {
		return ErrChainReused
	}
	if ChainKeyCipherDigest(d.ChainKeyCiphertext) != d.ChainKeyDigest {
		return ErrDigestMismatch
	}
	if d.StartIndex == math.MaxUint64 {
		return ErrIndexOverflow
	}

	// Persist key material before mutating any state. If this fails the state
	// machine is untouched.
	if err := s.store.Put(handle, initialChainKey); err != nil {
		return err
	}

	// Replacing a chain for an existing member deletes the old key handle;
	// the old chainID remains permanently registered as used.
	if old, exists := s.chains[d.SenderDevice]; exists {
		if old.handle != handle {
			_ = s.store.Delete(old.handle)
		}
	}

	s.chains[d.SenderDevice] = &senderChain{
		chainID:   d.ChainID,
		handle:    handle,
		nextIndex: d.StartIndex,
	}
	s.usedChainIDs[d.ChainID] = struct{}{}
	return nil
}

// Seal advances the sender's own chain by one step and returns the header,
// message key, nonce and ciphertext for a new data message. The message key
// and chain ratchet are derived only after the sender is confirmed as a
// member of the current epoch with an installed chain.
func (s *GroupState) Seal(sender DeviceID, plaintext []byte) (Header, []byte, []byte, []byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.members.Contains(sender) {
		return Header{}, nil, nil, nil, ErrNotMember
	}
	chain, ok := s.chains[sender]
	if !ok {
		return Header{}, nil, nil, nil, ErrChainNotFound
	}
	if chain.nextIndex == math.MaxUint64 {
		return Header{}, nil, nil, nil, ErrIndexOverflow
	}

	chainKey, err := s.store.Get(chain.handle)
	if err != nil {
		return Header{}, nil, nil, nil, err
	}

	index := chain.nextIndex
	messageKey := DeriveMessageKey(chainKey, index)
	nextChainKey := DeriveNextChainKey(chainKey, index)

	header := Header{
		GroupID:      append([]byte(nil), s.groupID...),
		Epoch:        s.epoch,
		SenderDevice: sender,
		ChainID:      chain.chainID,
		Index:        index,
	}
	aad := EncodeAAD(header, s.digest)
	nonce, ciphertext, err := Seal(messageKey, plaintext, aad)
	if err != nil {
		return Header{}, nil, nil, nil, err
	}

	// Commit the ratchet only after the message has been sealed successfully.
	if err := s.store.Put(chain.handle, nextChainKey); err != nil {
		return Header{}, nil, nil, nil, err
	}
	chain.nextIndex = index + 1

	return header, messageKey, nonce, ciphertext, nil
}

// Open authenticates a data message and, only on success, advances state and
// invokes callback. Authentication failure never advances a chain or consumes
// a cached key. The callback never runs unless the sender belongs to the
// signed member set recorded for the message epoch.
func (s *GroupState) Open(header Header, nonce, ciphertext []byte, callback SuccessCallback) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !bytes.Equal(header.GroupID, s.groupID) {
		return ErrGroupID
	}
	if header.Epoch != s.epoch {
		// Every non-current epoch is rejected permanently, including future
		// epochs whose distribution has not been applied yet.
		return ErrOldEpoch
	}

	info, known := s.history[header.Epoch]
	if !known || !info.members.Contains(header.SenderDevice) {
		return ErrNotMember
	}

	// A cached skipped key, if present, takes precedence and is single-use.
	for i := range s.skipped {
		entry := &s.skipped[i]
		if entry.sender == header.SenderDevice &&
			entry.chainID == header.ChainID &&
			entry.index == header.Index {
			aad := EncodeAAD(header, info.digest)
			plaintext, err := Open(entry.key, nonce, ciphertext, aad)
			if err != nil {
				// Authentication failed: keep the cached key untouched.
				return err
			}
			// Consume the one-time key only after successful authentication.
			zeroize(entry.key)
			s.skipped = append(s.skipped[:i], s.skipped[i+1:]...)
			return callback(header.SenderDevice, header, plaintext)
		}
	}

	chain, ok := s.chains[header.SenderDevice]
	if !ok {
		return ErrChainNotFound
	}
	if chain.chainID != header.ChainID {
		return ErrChainMismatch
	}

	if header.Index < chain.nextIndex {
		// An already-consumed index that was not served from the skipped
		// cache is a replay.
		return ErrDuplicate
	}
	if header.Index == math.MaxUint64 {
		return ErrIndexOverflow
	}

	currentKey, err := s.store.Get(chain.handle)
	if err != nil {
		return err
	}

	// Derive every key needed up to the message index into local variables
	// first. Nothing is persisted until AEAD authentication succeeds.
	gap := header.Index - chain.nextIndex
	if uint64(len(s.skipped))+gap > MaxSkippedKeys {
		return ErrSkippedLimit
	}

	var (
		newEntries []skippedKey
		runningKey = currentKey
		targetKey  []byte
		nextKey    []byte
	)
	for step := uint64(0); step <= gap; step++ {
		index := chain.nextIndex + step
		messageKey := DeriveMessageKey(runningKey, index)
		if step == gap {
			targetKey = messageKey
			nextKey = DeriveNextChainKey(runningKey, index)
			break
		}
		newEntries = append(newEntries, skippedKey{
			sender:  header.SenderDevice,
			chainID: chain.chainID,
			index:   index,
			key:     messageKey,
		})
		runningKey = DeriveNextChainKey(runningKey, index)
	}

	aad := EncodeAAD(header, info.digest)
	plaintext, err := Open(targetKey, nonce, ciphertext, aad)
	if err != nil {
		for _, entry := range newEntries {
			zeroize(entry.key)
		}
		return err
	}

	// Authentication succeeded: commit the ratchet, the skipped cache and the
	// handle atomically under the state lock.
	if err := s.store.Put(chain.handle, nextKey); err != nil {
		for _, entry := range newEntries {
			zeroize(entry.key)
		}
		return err
	}
	s.skipped = append(s.skipped, newEntries...)
	chain.nextIndex = header.Index + 1

	return callback(header.SenderDevice, header, plaintext)
}

// AdvanceEpoch applies a member-set change: the epoch increases by exactly
// one, every sender chain of the old epoch is revoked and its key handle
// deleted, and the skipped-key cache is cleared. Members that remain must
// receive a fresh distribution before they can seal again.
func (s *GroupState) AdvanceEpoch(newMembers MemberSet) (uint64, Digest, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	next := newMembers.Clone()
	digest := next.Digest()
	newEpoch := s.epoch + 1

	// Delete all old chain key material before dropping the chains.
	for _, chain := range s.chains {
		if err := s.store.Delete(chain.handle); err != nil {
			return 0, Digest{}, err
		}
	}

	for _, entry := range s.skipped {
		zeroize(entry.key)
	}

	s.epoch = newEpoch
	s.members = next
	s.digest = digest
	s.chains = make(map[DeviceID]*senderChain)
	s.skipped = nil
	s.history[newEpoch] = epochInfo{members: next, digest: digest}

	return newEpoch, digest, nil
}

func zeroize(buf []byte) {
	for i := range buf {
		buf[i] = 0
	}
}
