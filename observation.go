package netsec

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/json"
	"fmt"
)

const observationDomain = "netsec/device-list-observation/v1"

// Observation is the portable, signed statement exchanged between contacts.
type Observation struct {
	AccountID        AccountID `json:"accountID"`
	Version          Version   `json:"version"`
	ListDigest       Digest    `json:"listDigest"`
	CheckpointDigest Digest    `json:"checkpointDigest"`
}

// DeviceObservation is an alias for Observation.
type DeviceObservation = Observation

// SignedObservation is an observation receipt signed by a contact.
type SignedObservation struct {
	ObserverID  ContactID   `json:"observerID"`
	Observation Observation `json:"observation"`
	Signature   []byte      `json:"signature"`
}

// ObservationReceipt is an alias for SignedObservation.
type ObservationReceipt = SignedObservation

// SignObservation creates a contact-signed observation receipt.
func SignObservation(observerID ContactID, contactPrivateKey ed25519.PrivateKey, observation Observation) (*SignedObservation, error) {
	if len(contactPrivateKey) != ed25519.PrivateKeySize {
		return nil, fmt.Errorf("%w: contact private key has %d bytes", ErrInvalidIdentityKey, len(contactPrivateKey))
	}
	if err := observation.Validate(); err != nil {
		return nil, err
	}
	message := canonicalObservation(observerID, observation)
	signature := ed25519.Sign(contactPrivateKey, message)
	return &SignedObservation{
		ObserverID:  observerID,
		Observation: observation,
		Signature:   append([]byte(nil), signature...),
	}, nil
}

// Validate checks the static fields of an observation.
func (o Observation) Validate() error {
	if o.AccountID == "" {
		return ErrInvalidAccountID
	}
	if o.Version == 0 {
		return fmt.Errorf("%w: observation version 0", ErrInvalidVersion)
	}
	if o.ListDigest == ZeroDigest {
		return fmt.Errorf("%w: observation has no list digest", ErrEvidenceInvalid)
	}
	if o.CheckpointDigest == ZeroDigest {
		return fmt.Errorf("%w: observation has no checkpoint digest", ErrEvidenceInvalid)
	}
	return nil
}

func canonicalObservation(observerID ContactID, observation Observation) []byte {
	buf := make([]byte, 0, 128)
	buf = appendLengthPrefixed(buf, []byte(observationDomain))
	buf = appendLengthPrefixed(buf, []byte(observerID))
	buf = appendLengthPrefixed(buf, []byte(observation.AccountID))
	buf = appendUint64(buf, uint64(observation.Version))
	buf = append(buf, observation.ListDigest[:]...)
	buf = append(buf, observation.CheckpointDigest[:]...)
	return buf
}

func appendUint64(dst []byte, value uint64) []byte {
	var raw [8]byte
	// Kept local to avoid exposing an encoding detail through types.go.
	for i := 7; i >= 0; i-- {
		raw[i] = byte(value)
		value >>= 8
	}
	return append(dst, raw[:]...)
}

// Verify checks that the receipt is for the expected account and was signed by
// the supplied contact key.
func (r *SignedObservation) Verify(accountID AccountID, contactPublicKey ed25519.PublicKey) error {
	if r == nil {
		return fmt.Errorf("%w: nil receipt", ErrObservationSignature)
	}
	if len(contactPublicKey) != ed25519.PublicKeySize {
		return fmt.Errorf("%w: contact public key has %d bytes", ErrInvalidIdentityKey, len(contactPublicKey))
	}
	if r.Observation.AccountID != accountID {
		return ErrObservationAccount
	}
	if err := r.Observation.Validate(); err != nil {
		return err
	}
	if len(r.Signature) != ed25519.SignatureSize {
		return fmt.Errorf("%w: signature has %d bytes", ErrObservationSignature, len(r.Signature))
	}
	if !ed25519.Verify(contactPublicKey, canonicalObservation(r.ObserverID, r.Observation), r.Signature) {
		return ErrObservationSignature
	}
	return nil
}

// VerifyObservation is a nil-safe convenience wrapper.
func VerifyObservation(receipt *SignedObservation, accountID AccountID, contactPublicKey ed25519.PublicKey) error {
	if receipt == nil {
		return fmt.Errorf("%w: nil receipt", ErrObservationSignature)
	}
	return receipt.Verify(accountID, contactPublicKey)
}

// Clone returns a deep copy.
func (r *SignedObservation) Clone() *SignedObservation {
	if r == nil {
		return nil
	}
	return &SignedObservation{
		ObserverID:  r.ObserverID,
		Observation: r.Observation,
		Signature:   append([]byte(nil), r.Signature...),
	}
}

// Observe creates a receipt for the client's committed version. The contact
// private key is supplied by the host; the client does not retain it.
func (c *Client) Observe(observerID ContactID, contactPrivateKey ed25519.PrivateKey) (*SignedObservation, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.hasState || !c.hasCheckpoint {
		return nil, fmt.Errorf("%w: no committed state to observe", ErrStaleObservation)
	}
	observation := Observation{
		AccountID:        c.account,
		Version:          c.version,
		ListDigest:       c.digest,
		CheckpointDigest: c.checkpoint.Digest(),
	}
	receipt, err := SignObservation(observerID, contactPrivateKey, observation)
	if err != nil {
		return nil, err
	}
	c.recordObservationLocked(receipt)
	return receipt.Clone(), nil
}

// CreateObservation is an alias for Observe.
func (c *Client) CreateObservation(observerID ContactID, contactPrivateKey ed25519.PrivateKey) (*SignedObservation, error) {
	return c.Observe(observerID, contactPrivateKey)
}

// ObservationStatus describes how a received receipt compares to local state
// and previously received observations.
type ObservationStatus string

const (
	ObservationAccepted  ObservationStatus = "accepted"
	ObservationDuplicate ObservationStatus = "duplicate"
	ObservationStale     ObservationStatus = "stale"
	ObservationConflict  ObservationStatus = "conflict"
)

const (
	ObservationOK       = ObservationAccepted
	ObservationOutdated = ObservationStale
)

// ObservationResult describes receipt processing.
type ObservationResult struct {
	Status   ObservationStatus
	Version  Version
	Digest   Digest
	Stale    bool
	Frozen   bool
	Evidence *ForkEvidence
}

// IsStale reports whether the receipt described an older observation.
func (r ObservationResult) IsStale() bool {
	return r.Status == ObservationStale
}

// CheckObservation verifies and records a receipt. A receipt for an old
// version is recorded as stale. A same-version digest disagreement, or two
// different list digests anchored at the same checkpoint, freezes new
// sessions and returns exportable evidence.
func (c *Client) CheckObservation(receipt *SignedObservation, contactPublicKey ed25519.PublicKey) (ObservationResult, error) {
	if err := receipt.Verify(c.account, contactPublicKey); err != nil {
		return ObservationResult{}, err
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	if c.frozen {
		return ObservationResult{
			Status:   ObservationConflict,
			Version:  receipt.Observation.Version,
			Digest:   receipt.Observation.ListDigest,
			Frozen:   true,
			Evidence: c.forkEvidence.Clone(),
		}, ErrForkFrozen
	}

	observation := receipt.Observation
	if previous, ok := c.observations[observation.Version]; ok &&
		previous.Observation.ListDigest != observation.ListDigest {
		return c.freezeObservationConflictLocked(previous, *receipt, ForkSameVersionDifferentDigest), ErrForkDetected
	}
	if record, ok := c.history[observation.Version]; ok && record.digest != observation.ListDigest {
		first := ConflictWitness{
			SignedList: record.signed.Clone(),
			Proof:      cloneProof(record.proof),
		}
		evidence := c.makeObservationEvidenceLocked(
			ForkSameVersionDifferentDigest,
			observation.Version,
			first,
			ConflictWitness{Observation: receipt.Clone()},
		)
		c.frozen = true
		c.forkEvidence = evidence
		return ObservationResult{
			Status:   ObservationConflict,
			Version:  observation.Version,
			Digest:   observation.ListDigest,
			Frozen:   true,
			Evidence: evidence.Clone(),
		}, ErrForkDetected
	}
	if c.pending != nil &&
		c.pending.signed.List.Version == observation.Version &&
		c.pending.digest != observation.ListDigest {
		first := ConflictWitness{
			SignedList: c.pending.signed.Clone(),
			Proof:      cloneProof(c.pending.proof),
		}
		evidence := c.makeObservationEvidenceLocked(
			ForkSameVersionDifferentDigest,
			observation.Version,
			first,
			ConflictWitness{Observation: receipt.Clone()},
		)
		c.frozen = true
		c.forkEvidence = evidence
		return ObservationResult{
			Status:   ObservationConflict,
			Version:  observation.Version,
			Digest:   observation.ListDigest,
			Frozen:   true,
			Evidence: evidence.Clone(),
		}, ErrForkDetected
	}
	if previous, ok := c.checkpointSeen[observation.CheckpointDigest]; ok &&
		previous.Observation.ListDigest != observation.ListDigest {
		return c.freezeObservationConflictLocked(previous, *receipt, ForkSameCheckpointDifferentList), ErrForkDetected
	}

	stale := observation.Version < c.highestObs ||
		(c.hasState && observation.Version < c.version)
	if previous, ok := c.observations[observation.Version]; ok &&
		previous.Observation.ListDigest == observation.ListDigest {
		c.recordObservationLocked(receipt)
		return ObservationResult{
			Status:  ObservationDuplicate,
			Version: observation.Version,
			Digest:  observation.ListDigest,
			Stale:   stale,
		}, nil
	}

	c.recordObservationLocked(receipt)
	status := ObservationAccepted
	if stale {
		status = ObservationStale
	}
	return ObservationResult{
		Status:  status,
		Version: observation.Version,
		Digest:  observation.ListDigest,
		Stale:   stale,
	}, nil
}

func (c *Client) recordObservationLocked(receipt *SignedObservation) {
	observation := receipt.Observation
	if existing, ok := c.observations[observation.Version]; !ok ||
		existing.Observation.ListDigest == observation.ListDigest {
		c.observations[observation.Version] = *receipt.Clone()
	}
	if _, ok := c.checkpointSeen[observation.CheckpointDigest]; !ok {
		c.checkpointSeen[observation.CheckpointDigest] = *receipt.Clone()
	}
	if observation.Version > c.highestObs {
		c.highestObs = observation.Version
	}
}

func (c *Client) freezeObservationConflictLocked(first, second SignedObservation, reason ForkReason) ObservationResult {
	evidence := c.makeObservationEvidenceLocked(
		reason,
		second.Observation.Version,
		ConflictWitness{Observation: first.Clone()},
		ConflictWitness{Observation: second.Clone()},
	)
	c.frozen = true
	c.forkEvidence = evidence
	return ObservationResult{
		Status:   ObservationConflict,
		Version:  second.Observation.Version,
		Digest:   second.Observation.ListDigest,
		Frozen:   true,
		Evidence: evidence.Clone(),
	}
}

func (c *Client) makeObservationEvidenceLocked(reason ForkReason, version Version, first, second ConflictWitness) *ForkEvidence {
	return &ForkEvidence{
		AccountID: c.account,
		Reason:    reason,
		Version:   version,
		First:     first,
		Second:    second,
	}
}

// ForkReason identifies why evidence proves equivocation.
type ForkReason string

const (
	ForkSameVersionDifferentDigest  ForkReason = "same-version-different-digest"
	ForkSameCheckpointDifferentList ForkReason = "same-checkpoint-different-list"
	ForkSignedListsConflict         ForkReason = "signed-lists-conflict"
)

// ConflictWitness is one independently verifiable side of a fork. An
// observation receipt is preferred; a signed list and optional proof are used
// when the local client observed the other side directly.
type ConflictWitness struct {
	Observation *SignedObservation `json:"observation,omitempty"`
	SignedList  *SignedDeviceList  `json:"signedList,omitempty"`
	Proof       *LogInclusionProof `json:"proof,omitempty"`
}

func (w ConflictWitness) empty() bool {
	return w.Observation == nil && w.SignedList == nil
}

// ForkEvidence is a portable record of conflicting signed observations or
// signed lists.
type ForkEvidence struct {
	AccountID AccountID       `json:"accountID"`
	Reason    ForkReason      `json:"reason"`
	Version   Version         `json:"version"`
	First     ConflictWitness `json:"first"`
	Second    ConflictWitness `json:"second"`
}

// Evidence is an alias for ForkEvidence.
type Evidence = ForkEvidence

// Clone returns a deep copy.
func (e *ForkEvidence) Clone() *ForkEvidence {
	if e == nil {
		return nil
	}
	out := *e
	out.First = cloneWitness(e.First)
	out.Second = cloneWitness(e.Second)
	return &out
}

func cloneWitness(w ConflictWitness) ConflictWitness {
	return ConflictWitness{
		Observation: w.Observation.Clone(),
		SignedList:  w.SignedList.Clone(),
		Proof:       cloneProof(w.Proof),
	}
}

// Validate checks the structural shape of evidence. Callers that know the
// contacts' keys should additionally verify the embedded signatures.
func (e *ForkEvidence) Validate() error {
	if e == nil {
		return ErrEvidenceInvalid
	}
	if e.AccountID == "" {
		return fmt.Errorf("%w: missing account ID", ErrEvidenceInvalid)
	}
	if e.Version == 0 {
		return fmt.Errorf("%w: missing version", ErrEvidenceInvalid)
	}
	if e.Reason == "" {
		return fmt.Errorf("%w: missing fork reason", ErrEvidenceInvalid)
	}
	if e.First.empty() || e.Second.empty() {
		return fmt.Errorf("%w: both witnesses are required", ErrEvidenceInvalid)
	}
	if e.First.Observation != nil {
		if err := e.First.Observation.Observation.Validate(); err != nil {
			return fmt.Errorf("%w: first witness: %v", ErrEvidenceInvalid, err)
		}
		if e.First.Observation.Observation.AccountID != e.AccountID {
			return fmt.Errorf("%w: first receipt is for another account", ErrEvidenceInvalid)
		}
	}
	if e.Second.Observation != nil {
		if err := e.Second.Observation.Observation.Validate(); err != nil {
			return fmt.Errorf("%w: second witness: %v", ErrEvidenceInvalid, err)
		}
		if e.Second.Observation.Observation.AccountID != e.AccountID {
			return fmt.Errorf("%w: second receipt is for another account", ErrEvidenceInvalid)
		}
	}
	if e.First.SignedList != nil {
		if err := e.First.SignedList.List.Validate(); err != nil {
			return fmt.Errorf("%w: first signed list: %v", ErrEvidenceInvalid, err)
		}
		if e.First.SignedList.List.AccountID != e.AccountID {
			return fmt.Errorf("%w: first signed list is for another account", ErrEvidenceInvalid)
		}
	}
	if e.Second.SignedList != nil {
		if err := e.Second.SignedList.List.Validate(); err != nil {
			return fmt.Errorf("%w: second signed list: %v", ErrEvidenceInvalid, err)
		}
		if e.Second.SignedList.List.AccountID != e.AccountID {
			return fmt.Errorf("%w: second signed list is for another account", ErrEvidenceInvalid)
		}
	}
	if e.First.Proof != nil {
		if err := e.First.Proof.Checkpoint.Validate(); err != nil {
			return fmt.Errorf("%w: first proof: %v", ErrEvidenceInvalid, err)
		}
	}
	if e.Second.Proof != nil {
		if err := e.Second.Proof.Checkpoint.Validate(); err != nil {
			return fmt.Errorf("%w: second proof: %v", ErrEvidenceInvalid, err)
		}
	}
	return nil
}

// Verify checks the embedded account-master signatures and contact receipt
// signatures, validates any inclusion proof, and confirms that the witnesses
// actually conflict.
func (e *ForkEvidence) Verify(masterPublicKey ed25519.PublicKey, contacts map[ContactID]ed25519.PublicKey) error {
	if err := e.Validate(); err != nil {
		return err
	}
	if len(masterPublicKey) != ed25519.PublicKeySize {
		return fmt.Errorf("%w: master public key has %d bytes", ErrInvalidIdentityKey, len(masterPublicKey))
	}
	if err := verifyWitness(e.AccountID, e.First, masterPublicKey, contacts); err != nil {
		return fmt.Errorf("%w: first witness: %v", ErrEvidenceInvalid, err)
	}
	if err := verifyWitness(e.AccountID, e.Second, masterPublicKey, contacts); err != nil {
		return fmt.Errorf("%w: second witness: %v", ErrEvidenceInvalid, err)
	}
	firstVersion, firstDigest, firstCheckpoint := witnessIdentity(e.First)
	secondVersion, secondDigest, secondCheckpoint := witnessIdentity(e.Second)
	switch e.Reason {
	case ForkSameVersionDifferentDigest:
		if firstVersion != secondVersion || firstDigest == secondDigest {
			return fmt.Errorf("%w: witnesses are not a same-version fork", ErrEvidenceInvalid)
		}
	case ForkSameCheckpointDifferentList:
		if firstCheckpoint == ZeroDigest || firstCheckpoint != secondCheckpoint || firstDigest == secondDigest {
			return fmt.Errorf("%w: witnesses are not a same-checkpoint fork", ErrEvidenceInvalid)
		}
	case ForkSignedListsConflict:
		if firstVersion != secondVersion || firstDigest == secondDigest {
			return fmt.Errorf("%w: witnesses are not conflicting signed lists", ErrEvidenceInvalid)
		}
	default:
		return fmt.Errorf("%w: unknown fork reason %q", ErrEvidenceInvalid, e.Reason)
	}
	return nil
}

func verifyWitness(accountID AccountID, witness ConflictWitness, masterPublicKey ed25519.PublicKey, contacts map[ContactID]ed25519.PublicKey) error {
	if witness.Observation != nil {
		publicKey, ok := contacts[witness.Observation.ObserverID]
		if !ok {
			return fmt.Errorf("no public key for observer %q", witness.Observation.ObserverID)
		}
		if err := witness.Observation.Verify(accountID, publicKey); err != nil {
			return err
		}
	}
	if witness.SignedList != nil {
		if err := witness.SignedList.VerifyForAccount(accountID, masterPublicKey); err != nil {
			return err
		}
	}
	if witness.Proof != nil {
		if witness.SignedList != nil {
			digest, err := witness.SignedList.Digest()
			if err != nil {
				return err
			}
			if err := ValidateListProof(*witness.Proof, digest); err != nil {
				return err
			}
		} else if err := VerifyInclusionProof(*witness.Proof); err != nil {
			return err
		}
	}
	return nil
}

func witnessIdentity(witness ConflictWitness) (Version, Digest, Digest) {
	if witness.Observation != nil {
		observation := witness.Observation.Observation
		return observation.Version, observation.ListDigest, observation.CheckpointDigest
	}
	if witness.SignedList != nil {
		digest, _ := witness.SignedList.Digest()
		var checkpoint Digest
		if witness.Proof != nil {
			checkpoint = witness.Proof.Checkpoint.Digest()
		}
		return witness.SignedList.List.Version, digest, checkpoint
	}
	return 0, Digest{}, Digest{}
}

// Export serializes the evidence for transport or archival.
func (e ForkEvidence) Export() ([]byte, error) {
	if err := e.Validate(); err != nil {
		return nil, err
	}
	return json.Marshal(e)
}

// ExportForkEvidence is an alias for Export.
func ExportForkEvidence(evidence *ForkEvidence) ([]byte, error) {
	if evidence == nil {
		return nil, ErrEvidenceInvalid
	}
	return evidence.Export()
}

// ParseForkEvidence decodes exported evidence.
func ParseForkEvidence(data []byte) (*ForkEvidence, error) {
	var evidence ForkEvidence
	if err := json.Unmarshal(data, &evidence); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrEvidenceInvalid, err)
	}
	if err := evidence.Validate(); err != nil {
		return nil, err
	}
	return &evidence, nil
}

// Digest returns a stable SHA-256 digest of the exported evidence.
func (e ForkEvidence) Digest() (Digest, error) {
	exported, err := e.Export()
	if err != nil {
		return Digest{}, err
	}
	return sha256.Sum256(exported), nil
}

// Equal reports whether two observations are identical.
func (o Observation) Equal(other Observation) bool {
	return o.AccountID == other.AccountID &&
		o.Version == other.Version &&
		o.ListDigest == other.ListDigest &&
		o.CheckpointDigest == other.CheckpointDigest
}

// ReceiptsConflict reports whether two signed observations are directly
// mutually exclusive: same version/different list or same checkpoint/different
// list.
func ReceiptsConflict(first, second SignedObservation) (ForkReason, bool) {
	a := first.Observation
	b := second.Observation
	if a.AccountID != b.AccountID {
		return "", false
	}
	if a.Version == b.Version && a.ListDigest != b.ListDigest {
		return ForkSameVersionDifferentDigest, true
	}
	if a.CheckpointDigest == b.CheckpointDigest && a.ListDigest != b.ListDigest {
		return ForkSameCheckpointDifferentList, true
	}
	return "", false
}

// ReceiptDigest returns a digest over a receipt's canonical signed bytes. It is
// useful for deduplication; it is not a substitute for signature verification.
func ReceiptDigest(receipt SignedObservation) Digest {
	return sha256.Sum256(canonicalObservation(receipt.ObserverID, receipt.Observation))
}

// ReceiptEqual reports whether two receipts carry the same bytes.
func ReceiptEqual(first, second SignedObservation) bool {
	return first.ObserverID == second.ObserverID &&
		first.Observation.Equal(second.Observation) &&
		bytes.Equal(first.Signature, second.Signature)
}
