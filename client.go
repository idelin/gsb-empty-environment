package netsec

import (
	"crypto/ed25519"
	"errors"
	"fmt"
	"strings"
	"sync"
)

// DeviceListUpdate is a signed list plus an optional transparency-log inclusion
// proof. A nil proof stages the list without authorizing new devices.
type DeviceListUpdate struct {
	SignedList *SignedDeviceList  `json:"signedList"`
	Proof      *LogInclusionProof `json:"proof,omitempty"`
}

// Update is an alias for DeviceListUpdate.
type Update = DeviceListUpdate

// UpdateStatus describes the outcome of applying a list update.
type UpdateStatus string

const (
	UpdateCommitted    UpdateStatus = "committed"
	UpdatePendingProof UpdateStatus = "pending-proof"
	UpdateStale        UpdateStatus = "stale"
	UpdateFrozen       UpdateStatus = "frozen"
)

const (
	StatusCommitted    = UpdateCommitted
	StatusPendingProof = UpdatePendingProof
	StatusStale        = UpdateStale
	StatusFrozen       = UpdateFrozen
	UpdateAccepted     = UpdateCommitted
)

// UpdateResult describes how an update affected the client. Stale updates and
// updates waiting for a log proof are normal outcomes and do not return errors.
type UpdateResult struct {
	Status           UpdateStatus
	Version          Version
	Digest           Digest
	CheckpointDigest Digest
	Pending          bool
	Frozen           bool
	Evidence         *ForkEvidence
	Reason           string
}

// ApplyResult is an alias for UpdateResult.
type ApplyResult = UpdateResult

// Committed reports whether the update was committed.
func (r UpdateResult) Committed() bool {
	return r.Status == UpdateCommitted
}

// ClientOptions configures a consistency client.
type ClientOptions struct {
	AccountID       AccountID
	MasterPublicKey ed25519.PublicKey
	ProofVerifier   ProofVerifier
	Initial         *ClientSnapshot
}

// ClientConfig is an alias for ClientOptions.
type ClientConfig = ClientOptions

// ClientSnapshot is a persisted, previously verified client state.
type ClientSnapshot struct {
	SignedList *SignedDeviceList `json:"signedList"`
	Checkpoint LogCheckpoint     `json:"checkpoint"`
	Frozen     bool              `json:"frozen,omitempty"`
	Evidence   *ForkEvidence     `json:"evidence,omitempty"`
}

type versionRecord struct {
	signed     *SignedDeviceList
	proof      *LogInclusionProof
	digest     Digest
	checkpoint LogCheckpoint
}

type pendingUpdate struct {
	signed *SignedDeviceList
	proof  *LogInclusionProof
	digest Digest
}

// Client pins the highest accepted device-list version and digest, the latest
// accepted log checkpoint, and the complete committed list used to validate
// transitions. All methods are safe for concurrent use.
type Client struct {
	mu       sync.Mutex
	account  AccountID
	masterPK ed25519.PublicKey
	verifier ProofVerifier

	state          AccountState
	version        Version
	digest         Digest
	hasState       bool
	checkpoint     LogCheckpoint
	hasCheckpoint  bool
	lastSigned     *SignedDeviceList
	lastProof      *LogInclusionProof
	pending        *pendingUpdate
	frozen         bool
	forkEvidence   *ForkEvidence
	history        map[Version]versionRecord
	observations   map[Version]SignedObservation
	checkpointSeen map[Digest]SignedObservation
	highestObs     Version
	nextSession    uint64
}

// NewClient creates a client at version zero. The first accepted list must be
// a version-1 genesis list. A nil or omitted proof verifier uses
// BuiltinProofVerifier.
func NewClient(accountID AccountID, masterPublicKey ed25519.PublicKey, verifiers ...ProofVerifier) (*Client, error) {
	if len(verifiers) > 1 {
		return nil, fmt.Errorf("netsec: NewClient accepts at most one proof verifier")
	}
	var verifier ProofVerifier
	if len(verifiers) == 1 {
		verifier = verifiers[0]
	}
	return NewClientWithOptions(ClientOptions{
		AccountID:       accountID,
		MasterPublicKey: masterPublicKey,
		ProofVerifier:   verifier,
	})
}

// NewClientWithOptions creates a client, optionally restoring a verified
// snapshot from secure storage.
func NewClientWithOptions(options ClientOptions) (*Client, error) {
	if strings.TrimSpace(string(options.AccountID)) == "" {
		return nil, ErrInvalidAccountID
	}
	if len(options.MasterPublicKey) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("%w: master public key has %d bytes", ErrInvalidIdentityKey, len(options.MasterPublicKey))
	}
	verifier := options.ProofVerifier
	if verifier == nil {
		verifier = BuiltinProofVerifier{}
	}
	c := &Client{
		account:        options.AccountID,
		masterPK:       append(ed25519.PublicKey(nil), options.MasterPublicKey...),
		verifier:       verifier,
		history:        make(map[Version]versionRecord),
		observations:   make(map[Version]SignedObservation),
		checkpointSeen: make(map[Digest]SignedObservation),
	}
	if options.Initial != nil {
		if err := c.restoreSnapshot(options.Initial); err != nil {
			return nil, err
		}
	}
	return c, nil
}

// NewClientFromSnapshot creates a client from a previously verified snapshot.
func NewClientFromSnapshot(
	accountID AccountID,
	masterPublicKey ed25519.PublicKey,
	snapshot ClientSnapshot,
	verifiers ...ProofVerifier,
) (*Client, error) {
	if len(verifiers) > 1 {
		return nil, fmt.Errorf("netsec: NewClientFromSnapshot accepts at most one proof verifier")
	}
	var verifier ProofVerifier
	if len(verifiers) == 1 {
		verifier = verifiers[0]
	}
	return NewClientWithOptions(ClientOptions{
		AccountID:       accountID,
		MasterPublicKey: masterPublicKey,
		ProofVerifier:   verifier,
		Initial:         &snapshot,
	})
}

func (c *Client) restoreSnapshot(snapshot *ClientSnapshot) error {
	if snapshot.Evidence != nil {
		if err := snapshot.Evidence.Validate(); err != nil {
			return err
		}
		if snapshot.Evidence.AccountID != c.account {
			return ErrAccountMismatch
		}
	}
	if snapshot.SignedList == nil {
		c.frozen = snapshot.Frozen || snapshot.Evidence != nil
		c.forkEvidence = snapshot.Evidence.Clone()
		return nil
	}
	if err := snapshot.SignedList.VerifyForAccount(c.account, c.masterPK); err != nil {
		return err
	}
	if err := snapshot.SignedList.List.Validate(); err != nil {
		return err
	}
	digest, err := snapshot.SignedList.List.Digest()
	if err != nil {
		return err
	}
	if err := snapshot.Checkpoint.Validate(); err != nil {
		return err
	}
	c.state = snapshot.SignedList.List.Clone()
	c.version = c.state.Version
	c.digest = digest
	c.hasState = true
	c.checkpoint = snapshot.Checkpoint
	c.hasCheckpoint = true
	c.lastSigned = snapshot.SignedList.Clone()
	c.history[c.version] = versionRecord{
		signed:     snapshot.SignedList.Clone(),
		digest:     digest,
		checkpoint: snapshot.Checkpoint,
	}
	c.frozen = snapshot.Frozen
	if snapshot.Evidence != nil {
		c.frozen = true
	}
	c.forkEvidence = snapshot.Evidence.Clone()
	return nil
}

// AccountID returns the account this client follows.
func (c *Client) AccountID() AccountID {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.account
}

// MasterPublicKey returns a copy of the pinned account master public key.
func (c *Client) MasterPublicKey() ed25519.PublicKey {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append(ed25519.PublicKey(nil), c.masterPK...)
}

// Version returns the highest committed list version.
func (c *Client) Version() Version {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.version
}

// CurrentVersion is an alias for Version.
func (c *Client) CurrentVersion() Version {
	return c.Version()
}

// Digest returns the highest committed list digest.
func (c *Client) Digest() Digest {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.digest
}

// CurrentDigest is an alias for Digest.
func (c *Client) CurrentDigest() Digest {
	return c.Digest()
}

// Checkpoint returns the latest committed log checkpoint.
func (c *Client) Checkpoint() (LogCheckpoint, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.checkpoint, c.hasCheckpoint
}

// List returns a deep copy of the committed account state.
func (c *Client) List() (AccountState, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.hasState {
		return AccountState{}, false
	}
	return c.state.Clone(), true
}

// State is an alias for List.
func (c *Client) State() (AccountState, bool) {
	return c.List()
}

// Frozen reports whether conflicting observations have frozen the client.
func (c *Client) Frozen() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.frozen
}

// ForkEvidence returns a copy of the evidence that froze the client.
func (c *Client) ForkEvidence() (*ForkEvidence, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.frozen || c.forkEvidence == nil {
		return nil, false
	}
	return c.forkEvidence.Clone(), true
}

// PendingUpdate reports whether a signed list is waiting for a log proof.
func (c *Client) PendingUpdate() (Version, Digest, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.pending == nil {
		return 0, Digest{}, false
	}
	return c.pending.signed.List.Version, c.pending.digest, true
}

// Snapshot exports the state needed to restore this client. The caller should
// protect the snapshot as trusted local state.
func (c *Client) Snapshot() (ClientSnapshot, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.hasState {
		return ClientSnapshot{
			Frozen:   c.frozen,
			Evidence: c.forkEvidence.Clone(),
		}, nil
	}
	return ClientSnapshot{
		SignedList: c.lastSigned.Clone(),
		Checkpoint: c.checkpoint,
		Frozen:     c.frozen,
		Evidence:   c.forkEvidence.Clone(),
	}, nil
}

// ApplyUpdate applies a signed list and, when supplied, its inclusion proof.
// If the proof is absent or the host marks it as not yet available, the list is
// staged but is not committed and no new device is authorized.
func (c *Client) ApplyUpdate(update DeviceListUpdate) (UpdateResult, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.applyUpdateLocked(update)
}

// Apply is a convenience wrapper around ApplyUpdate. The proof is optional.
func (c *Client) Apply(signed *SignedDeviceList, proofs ...*LogInclusionProof) (UpdateResult, error) {
	if len(proofs) > 1 {
		return UpdateResult{}, fmt.Errorf("netsec: Apply accepts at most one proof")
	}
	var proof *LogInclusionProof
	if len(proofs) == 1 {
		proof = proofs[0]
	}
	return c.ApplyUpdate(DeviceListUpdate{SignedList: signed, Proof: proof})
}

// SubmitUpdate is an alias for ApplyUpdate.
func (c *Client) SubmitUpdate(update DeviceListUpdate) (UpdateResult, error) {
	return c.ApplyUpdate(update)
}

// ApplySignedList stages a signed list. It is useful when the directory
// delivers the list before the transparency-log proof.
func (c *Client) ApplySignedList(signed *SignedDeviceList) (UpdateResult, error) {
	return c.Apply(signed, nil)
}

// ApplyProof supplies the inclusion proof for the currently staged list.
func (c *Client) ApplyProof(proof *LogInclusionProof) (UpdateResult, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.frozen {
		return c.frozenResultLocked(), ErrForkFrozen
	}
	if c.pending == nil {
		return UpdateResult{Status: UpdateStale, Version: c.version, Digest: c.digest}, nil
	}
	if proof == nil {
		return UpdateResult{
			Status:  UpdatePendingProof,
			Version: c.pending.signed.List.Version,
			Digest:  c.pending.digest,
			Pending: true,
			Reason:  "log proof not supplied",
		}, nil
	}
	result, err := c.validateProofLocked(c.pending.signed, c.pending.digest, proof)
	if err != nil || !result.Committed() {
		return result, err
	}
	c.commitPendingLocked(proof)
	return result, nil
}

// RetryPendingProof is an alias for ApplyProof.
func (c *Client) RetryPendingProof(proof *LogInclusionProof) (UpdateResult, error) {
	return c.ApplyProof(proof)
}

func (c *Client) applyUpdateLocked(update DeviceListUpdate) (UpdateResult, error) {
	if c.frozen {
		return c.frozenResultLocked(), ErrForkFrozen
	}
	if update.SignedList == nil {
		return UpdateResult{}, fmt.Errorf("%w: nil signed list", ErrInvalidSignature)
	}
	signed := update.SignedList
	if err := signed.VerifyForAccount(c.account, c.masterPK); err != nil {
		return UpdateResult{}, err
	}
	next := signed.List
	if err := next.Validate(); err != nil {
		return UpdateResult{}, err
	}
	digest, err := next.Digest()
	if err != nil {
		return UpdateResult{}, err
	}
	if !c.hasState || next.Version >= c.version {
		if result, conflict := c.observationListConflictLocked(signed, digest, update.Proof); conflict {
			return result, ErrForkDetected
		}
	}

	if c.pending != nil {
		if next.Version == c.version {
			if digest == c.digest {
				return UpdateResult{Status: UpdateStale, Version: next.Version, Digest: digest}, nil
			}
			result := c.freezeSignedListConflictLocked(signed, update.Proof, ForkSameVersionDifferentDigest)
			return result, ErrForkDetected
		}
		if next.Version < c.version {
			return UpdateResult{Status: UpdateStale, Version: next.Version, Digest: digest}, nil
		}
		if next.Version != c.pending.signed.List.Version {
			return UpdateResult{}, fmt.Errorf("%w: pending version %d, received %d",
				ErrPendingUpdate, c.pending.signed.List.Version, next.Version)
		}
		if digest != c.pending.digest {
			result := c.freezePendingConflictLocked(signed, update.Proof)
			return result, ErrForkDetected
		}
		if update.Proof == nil {
			return UpdateResult{
				Status:  UpdatePendingProof,
				Version: next.Version,
				Digest:  digest,
				Pending: true,
				Reason:  "log proof not supplied",
			}, nil
		}
		result, err := c.validateProofLocked(signed, digest, update.Proof)
		if err != nil || !result.Committed() {
			if !result.Committed() && err == nil {
				c.pending.proof = cloneProof(update.Proof)
			}
			return result, err
		}
		c.commitPendingLocked(update.Proof)
		return result, nil
	}

	if c.hasState && next.Version == c.version {
		if digest == c.digest {
			return UpdateResult{Status: UpdateStale, Version: next.Version, Digest: digest}, nil
		}
		result := c.freezeSignedListConflictLocked(signed, update.Proof, ForkSameVersionDifferentDigest)
		return result, ErrForkDetected
	}
	if c.hasState && next.Version < c.version {
		return UpdateResult{Status: UpdateStale, Version: next.Version, Digest: digest}, nil
	}

	if !c.hasState {
		if err := next.ValidateGenesis(); err != nil {
			return UpdateResult{}, err
		}
		if next.Version != 1 {
			return UpdateResult{}, fmt.Errorf("%w: genesis version is %d", ErrVersionJump, next.Version)
		}
	} else {
		if err := ValidateTransition(c.state, next); err != nil {
			return UpdateResult{}, err
		}
	}

	pending := &pendingUpdate{signed: signed.Clone(), digest: digest}
	c.pending = pending
	if update.Proof == nil {
		return UpdateResult{
			Status:  UpdatePendingProof,
			Version: next.Version,
			Digest:  digest,
			Pending: true,
			Reason:  "log proof not supplied",
		}, nil
	}
	result, err := c.validateProofLocked(signed, digest, update.Proof)
	if err != nil || !result.Committed() {
		if !result.Committed() && err == nil {
			pending.proof = cloneProof(update.Proof)
		}
		if result.Version == 0 {
			result.Version = next.Version
			result.Digest = digest
		}
		return result, err
	}
	c.commitPendingLocked(update.Proof)
	return result, nil
}

func (c *Client) validateProofLocked(signed *SignedDeviceList, listDigest Digest, proof *LogInclusionProof) (UpdateResult, error) {
	if proof == nil {
		return UpdateResult{
			Status:  UpdatePendingProof,
			Version: signed.List.Version,
			Digest:  listDigest,
			Pending: true,
			Reason:  "log proof not supplied",
		}, nil
	}
	if err := proof.Checkpoint.Validate(); err != nil {
		return UpdateResult{}, err
	}
	if proof.LeafDigest != listDigest {
		return UpdateResult{}, ErrProofMismatch
	}
	if err := VerifyInclusionProof(*proof); err != nil {
		return UpdateResult{}, err
	}
	if err := c.checkCheckpointLocked(proof.Checkpoint); err != nil {
		return UpdateResult{}, err
	}
	if c.verifier != nil {
		err := c.verifier.VerifyProof(proof.Checkpoint, *proof, listDigest)
		if err != nil {
			if errors.Is(err, ErrProofPending) || errors.Is(err, ErrLogUnavailable) {
				return UpdateResult{
					Status:           UpdatePendingProof,
					Version:          signed.List.Version,
					Digest:           listDigest,
					CheckpointDigest: proof.Checkpoint.Digest(),
					Pending:          true,
					Reason:           err.Error(),
				}, nil
			}
			return UpdateResult{}, fmt.Errorf("%w: %v", ErrProofInvalid, err)
		}
	}
	return UpdateResult{
		Status:           UpdateCommitted,
		Version:          signed.List.Version,
		Digest:           listDigest,
		CheckpointDigest: proof.Checkpoint.Digest(),
	}, nil
}

func (c *Client) checkCheckpointLocked(next LogCheckpoint) error {
	if !c.hasCheckpoint {
		return nil
	}
	if c.checkpoint.LogID != "" && next.LogID != c.checkpoint.LogID {
		return ErrLogMismatch
	}
	if next.TreeSize < c.checkpoint.TreeSize {
		return fmt.Errorf("%w: saved tree size %d, received %d",
			ErrCheckpointRollback, c.checkpoint.TreeSize, next.TreeSize)
	}
	if next.TreeSize == c.checkpoint.TreeSize && next.RootHash != c.checkpoint.RootHash {
		return fmt.Errorf("%w: same tree size has a different root", ErrCheckpointConflict)
	}
	return nil
}

func (c *Client) commitPendingLocked(proof *LogInclusionProof) {
	pending := c.pending
	c.state = pending.signed.List.Clone()
	c.version = c.state.Version
	c.digest = pending.digest
	c.hasState = true
	c.checkpoint = proof.Checkpoint
	c.hasCheckpoint = true
	c.lastSigned = pending.signed.Clone()
	c.lastProof = cloneProof(proof)
	c.pending = nil
	c.rememberHistoryLocked(pending.signed, proof, pending.digest, proof.Checkpoint)
}

func (c *Client) rememberHistoryLocked(signed *SignedDeviceList, proof *LogInclusionProof, digest Digest, checkpoint LogCheckpoint) {
	record := versionRecord{
		signed:     signed.Clone(),
		proof:      cloneProof(proof),
		digest:     digest,
		checkpoint: checkpoint,
	}
	c.history[signed.List.Version] = record
	if signed.List.Version > c.highestObs {
		c.highestObs = signed.List.Version
	}
}

func cloneProof(proof *LogInclusionProof) *LogInclusionProof {
	if proof == nil {
		return nil
	}
	out := *proof
	out.AuditPath = make([]Digest, len(proof.AuditPath))
	copy(out.AuditPath, proof.AuditPath)
	return &out
}

func (c *Client) freezeSignedListConflictLocked(incoming *SignedDeviceList, incomingProof *LogInclusionProof, reason ForkReason) UpdateResult {
	first := ConflictWitness{}
	if c.lastSigned != nil {
		first.SignedList = c.lastSigned.Clone()
		first.Proof = cloneProof(c.lastProof)
	}
	second := ConflictWitness{
		SignedList: incoming.Clone(),
		Proof:      cloneProof(incomingProof),
	}
	evidence := &ForkEvidence{
		AccountID: c.account,
		Reason:    ForkReason(reason),
		Version:   incoming.List.Version,
		First:     first,
		Second:    second,
	}
	c.frozen = true
	c.forkEvidence = evidence
	return UpdateResult{
		Status:   UpdateFrozen,
		Version:  incoming.List.Version,
		Digest:   c.digest,
		Frozen:   true,
		Evidence: evidence.Clone(),
	}
}

func (c *Client) freezePendingConflictLocked(incoming *SignedDeviceList, incomingProof *LogInclusionProof) UpdateResult {
	first := ConflictWitness{}
	if c.pending != nil {
		first.SignedList = c.pending.signed.Clone()
		first.Proof = cloneProof(c.pending.proof)
	}
	second := ConflictWitness{
		SignedList: incoming.Clone(),
		Proof:      cloneProof(incomingProof),
	}
	evidence := &ForkEvidence{
		AccountID: c.account,
		Reason:    ForkSameVersionDifferentDigest,
		Version:   incoming.List.Version,
		First:     first,
		Second:    second,
	}
	c.frozen = true
	c.forkEvidence = evidence
	return UpdateResult{
		Status:   UpdateFrozen,
		Version:  incoming.List.Version,
		Digest:   c.digest,
		Frozen:   true,
		Evidence: evidence.Clone(),
	}
}

func (c *Client) observationListConflictLocked(incoming *SignedDeviceList, digest Digest, proof *LogInclusionProof) (UpdateResult, bool) {
	reason := ForkReason("")
	var observation *SignedObservation
	if previous, ok := c.observations[incoming.List.Version]; ok &&
		previous.Observation.ListDigest != digest {
		reason = ForkSameVersionDifferentDigest
		observation = previous.Clone()
	} else if proof != nil {
		if previous, ok := c.checkpointSeen[proof.Checkpoint.Digest()]; ok &&
			previous.Observation.ListDigest != digest {
			reason = ForkSameCheckpointDifferentList
			observation = previous.Clone()
		}
	}
	if observation == nil {
		return UpdateResult{}, false
	}
	evidence := &ForkEvidence{
		AccountID: c.account,
		Reason:    reason,
		Version:   incoming.List.Version,
		First:     ConflictWitness{Observation: observation},
		Second: ConflictWitness{
			SignedList: incoming.Clone(),
			Proof:      cloneProof(proof),
		},
	}
	c.frozen = true
	c.forkEvidence = evidence
	return UpdateResult{
		Status:   UpdateFrozen,
		Version:  incoming.List.Version,
		Digest:   digest,
		Frozen:   true,
		Evidence: evidence.Clone(),
	}, true
}

func (c *Client) frozenResultLocked() UpdateResult {
	return UpdateResult{
		Status:   UpdateFrozen,
		Version:  c.version,
		Digest:   c.digest,
		Frozen:   true,
		Evidence: c.forkEvidence.Clone(),
	}
}

// FreezeWithEvidence lets a host freeze the client after verifying fork
// evidence out of band. It never unfreezes a client.
func (c *Client) FreezeWithEvidence(evidence *ForkEvidence) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if evidence == nil {
		return ErrEvidenceInvalid
	}
	if err := evidence.Validate(); err != nil {
		return err
	}
	if evidence.AccountID != c.account {
		return ErrAccountMismatch
	}
	if c.frozen {
		return ErrForkFrozen
	}
	c.frozen = true
	c.forkEvidence = evidence.Clone()
	return nil
}

// Session is a logical message channel to a device. Authorization is checked
// dynamically, so committing a removal immediately disables new sends.
type Session struct {
	mu       sync.Mutex
	client   *Client
	deviceID DeviceID
	number   uint64
	closed   bool
}

// OpenSession creates a session to an active, fully authorized device. New
// devices from pending updates are not visible and therefore cannot be used.
func (c *Client) OpenSession(deviceID DeviceID) (*Session, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.frozen {
		return nil, ErrForkFrozen
	}
	if !c.hasState {
		return nil, ErrDeviceNotAuthorized
	}
	device, ok := c.state.Device(deviceID)
	if !ok {
		return nil, c.deviceLookupErrorLocked(deviceID)
	}
	if !device.Active() {
		return nil, ErrDeviceRemoved
	}
	c.nextSession++
	return &Session{
		client:   c,
		deviceID: deviceID,
		number:   c.nextSession,
	}, nil
}

// StartSession is an alias for OpenSession.
func (c *Client) StartSession(deviceID DeviceID) (*Session, error) {
	return c.OpenSession(deviceID)
}

// DeviceID returns the device associated with the session.
func (s *Session) DeviceID() DeviceID {
	if s == nil {
		return ""
	}
	return s.deviceID
}

// Close closes the session.
func (s *Session) Close() error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	return nil
}

// CanSend reports whether it is safe to start a new outbound message. A
// committed removal or a detected fork makes this false immediately.
func (s *Session) CanSend() bool {
	return s.SendAllowed() == nil
}

// CanReceive reports whether the device may deliver a new inbound message.
func (s *Session) CanReceive() bool {
	return s.ReceiveAllowed() == nil
}

// SendAllowed returns the reason a new send is not allowed.
func (s *Session) SendAllowed() error {
	if s == nil {
		return ErrSessionClosed
	}
	s.mu.Lock()
	closed := s.closed
	s.mu.Unlock()
	if closed {
		return ErrSessionClosed
	}
	return s.client.authorizeDevice(s.deviceID)
}

// ReceiveAllowed returns the reason a new receive is not allowed.
func (s *Session) ReceiveAllowed() error {
	return s.SendAllowed()
}

// Send is a placeholder for the host's actual transport. It performs the
// authorization check but deliberately does not transmit bytes.
func (s *Session) Send() error {
	return s.SendAllowed()
}

func (c *Client) authorizeDevice(deviceID DeviceID) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.frozen {
		return ErrForkFrozen
	}
	if !c.hasState {
		return ErrDeviceNotAuthorized
	}
	device, ok := c.state.Device(deviceID)
	if !ok {
		return c.deviceLookupErrorLocked(deviceID)
	}
	if !device.Active() {
		return ErrDeviceRemoved
	}
	return nil
}

func (c *Client) deviceLookupErrorLocked(deviceID DeviceID) error {
	if c.pending != nil {
		if _, ok := c.pending.signed.List.Device(deviceID); ok {
			return ErrDeviceNotAuthorized
		}
	}
	return ErrDeviceNotFound
}

// CanSendTo reports whether new sends to a device are currently allowed.
func (c *Client) CanSendTo(deviceID DeviceID) bool {
	return c.authorizeDevice(deviceID) == nil
}

// CanReceiveFrom reports whether a newly added device has completed both
// signature and log-proof verification.
func (c *Client) CanReceiveFrom(deviceID DeviceID) bool {
	return c.authorizeDevice(deviceID) == nil
}

// AcceptIncoming performs the receive-side authorization check for a host.
func (c *Client) AcceptIncoming(deviceID DeviceID) error {
	return c.authorizeDevice(deviceID)
}
