package netsec

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"
)

// CeremonySpec describes a one-time root-key import.
type CeremonySpec struct {
	Slot                  string
	Algorithm             string
	Purpose               string
	KeyMaterialCommitment [sha256.Size]byte
	Validity              time.Duration
}

// State is the externally observable lifecycle of an import ceremony.
type State string

const (
	StateCollecting State = "collecting"
	StateImporting  State = "importing"
	StateCompleted  State = "completed"
	StateFailed     State = "failed"
)

// CeremonyBinding is included in every approval, share AAD, HSM attribute
// digest, and receipt check.
type CeremonyBinding struct {
	CeremonyID            string
	Slot                  string
	Algorithm             string
	Purpose               string
	KeyMaterialCommitment [sha256.Size]byte
	WrappingKeyDigest     [sha256.Size]byte
	StartUnixNano         int64
	DeadlineUnixNano      int64
	PolicyVersion         uint64
}

// Decision is the only approval value accepted by the state machine.
type Decision string

const DecisionApprove Decision = "approve"

// Approval is an administrator's Ed25519 signature over the ceremony binding
// and the registry-issued credential/person identity.
type Approval struct {
	CredentialID string
	Decision     Decision
	Signature    []byte
}

// ApprovalSigningPayload returns the exact signed byte string. PersonID is
// taken from the HR-signed registry entry by the host when verifying.
func ApprovalSigningPayload(binding CeremonyBinding, credentialID, personID string, decision Decision) []byte {
	payload := canonicalField(nil, []byte("netsec.import-approval.v1"))
	payload = bindingSigningFields(payload, binding)
	payload = canonicalString(payload, credentialID)
	payload = canonicalString(payload, personID)
	payload = canonicalString(payload, string(decision))
	return payload
}

// SignApproval creates an approval using a real Ed25519 credential key.
func SignApproval(binding CeremonyBinding, credentialID, personID string, privateKey ed25519.PrivateKey) (Approval, error) {
	if len(privateKey) != ed25519.PrivateKeySize {
		return Approval{}, ErrInvalidArgument
	}
	if credentialID == "" || personID == "" || binding.CeremonyID == "" {
		return Approval{}, ErrInvalidArgument
	}
	return Approval{
		CredentialID: credentialID,
		Decision:     DecisionApprove,
		Signature: ed25519.Sign(
			privateKey,
			ApprovalSigningPayload(binding, credentialID, personID, DecisionApprove),
		),
	}, nil
}

func bindingSigningFields(dst []byte, binding CeremonyBinding) []byte {
	dst = canonicalString(dst, binding.CeremonyID)
	dst = canonicalString(dst, binding.Slot)
	dst = canonicalString(dst, binding.Algorithm)
	dst = canonicalString(dst, binding.Purpose)
	dst = canonicalDigest(dst, binding.KeyMaterialCommitment)
	dst = canonicalDigest(dst, binding.WrappingKeyDigest)
	dst = canonicalInt64(dst, binding.StartUnixNano)
	dst = canonicalInt64(dst, binding.DeadlineUnixNano)
	dst = canonicalUint64(dst, binding.PolicyVersion)
	return dst
}

// CeremonyStatus is a concurrency-safe snapshot of the ceremony counters.
type CeremonyStatus struct {
	CeremonyID        string
	State             State
	Reason            string
	ApprovalCount     int
	ShareCount        int
	RequiredApprovals int
	RequiredShares    int
	Start             time.Time
	Deadline          time.Time
}

// ImportRequest is the only material the host sends to the HSM import
// callback. It contains sealed shares, never plaintext share bytes.
type ImportRequest struct {
	Binding       CeremonyBinding
	WrappedShares []WrappedShare
}

// WrappedShare is the HSM-visible part of one participant's sealed share.
type WrappedShare struct {
	PersonID           string
	CredentialID       string
	EphemeralPublicKey []byte
	Nonce              []byte
	Ciphertext         []byte
}

// ImportResult is returned by the HSM and is reconciled against the ceremony
// binding before the ceremony becomes Completed.
type ImportResult struct {
	KeyHandle             string
	AttributesDigest      [sha256.Size]byte
	KeyMaterialCommitment [sha256.Size]byte
	Receipt               Receipt
}

// HSM is the narrow boundary implemented by a hardware security module or a
// test double. CreateImportKey must create a fresh X25519 receipt key for one
// ceremony. CombineAndImport receives only sealed shares.
type HSM interface {
	CreateImportKey(ctx context.Context, ceremonyID string) (publicKey []byte, err error)
	CombineAndImport(ctx context.Context, request ImportRequest) (ImportResult, error)
	ReadReceipt(ctx context.Context, ceremonyID string) (ImportResult, error)
}

// Clock supplies the monotonic timestamps used for start/deadline checks.
type Clock interface {
	Now() time.Time
}

type realClock struct{}

func (realClock) Now() time.Time { return time.Now() }

type approvalRecord struct {
	credentialID         string
	personID             string
	role                 Role
	revocationGeneration uint64
}

type shareRecord struct {
	personID             string
	credentialID         string
	role                 Role
	revocationGeneration uint64
	envelope             SealedShare
}

type credentialRef struct {
	credentialID         string
	personID             string
	role                 Role
	revocationGeneration uint64
}

// Ceremony is an import ceremony instance. Methods are safe for concurrent
// use. The host never stores a plaintext share.
type Ceremony struct {
	host              *Host
	binding           CeremonyBinding
	wrappingPublicKey []byte
	start             time.Time
	deadline          time.Time

	mu                 sync.Mutex
	state              State
	reason             error
	approvals          map[string]approvalRecord // keyed by immutable personID
	shares             map[string]shareRecord    // keyed by immutable personID
	importAttempted    bool
	recoverableReceipt bool
	importCredentials  []credentialRef
	importDone         chan struct{}
	importClosed       bool
	result             *ImportResult
}

// Host owns a registry, an HSM implementation, and all live ceremonies.
type Host struct {
	mu          sync.RWMutex
	registry    *Registry
	hsm         HSM
	receiptKey  ed25519.PublicKey
	clock       Clock
	ceremonies  map[string]*Ceremony
	unsubscribe func()
}

// NewHost creates a host that verifies every HSM receipt with receiptKey.
func NewHost(registry *Registry, hsm HSM, receiptKey ed25519.PublicKey, clock Clock) (*Host, error) {
	if registry == nil || hsm == nil || len(receiptKey) != ed25519.PublicKeySize {
		return nil, ErrInvalidArgument
	}
	if clock == nil {
		clock = realClock{}
	}
	h := &Host{
		registry:   registry,
		hsm:        hsm,
		receiptKey: append(ed25519.PublicKey(nil), receiptKey...),
		clock:      clock,
		ceremonies: make(map[string]*Ceremony),
	}
	h.unsubscribe = registry.subscribe(h.registryChanged)
	return h, nil
}

// Close stops observing registry changes. Live ceremonies are marked failed
// and their sealed shares are destroyed.
func (h *Host) Close() {
	if h.unsubscribe != nil {
		h.unsubscribe()
	}
	h.mu.RLock()
	ceremonies := make([]*Ceremony, 0, len(h.ceremonies))
	for _, ceremony := range h.ceremonies {
		ceremonies = append(ceremonies, ceremony)
	}
	h.mu.RUnlock()
	for _, ceremony := range ceremonies {
		ceremony.mu.Lock()
		if ceremony.state == StateCollecting || ceremony.state == StateImporting {
			ceremony.terminateLocked(ErrCeremonyClosed)
		}
		ceremony.mu.Unlock()
	}
}

// BeginCeremony generates a random ceremony ID, obtains a one-time X25519
// import key from the HSM, and binds the current policy generation and
// monotonic start/deadline values.
func (h *Host) BeginCeremony(ctx context.Context, spec CeremonySpec) (*Ceremony, error) {
	if ctx == nil {
		return nil, ErrInvalidArgument
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if spec.Slot == "" || spec.Algorithm == "" || spec.Purpose == "" || spec.Validity <= 0 {
		return nil, ErrInvalidArgument
	}
	policy := h.registry.policySnapshot()
	now := h.clock.Now()
	deadline := now.Add(spec.Validity)
	if !deadline.After(now) {
		return nil, ErrInvalidArgument
	}

	var ceremonyID string
	var wrappingPublicKey []byte
	var err error
	for attempt := 0; attempt < 8; attempt++ {
		idBytes := make([]byte, 16)
		if _, err = rand.Read(idBytes); err != nil {
			return nil, err
		}
		ceremonyID = hex.EncodeToString(idBytes)
		h.mu.RLock()
		_, exists := h.ceremonies[ceremonyID]
		h.mu.RUnlock()
		if exists {
			continue
		}
		wrappingPublicKey, err = h.hsm.CreateImportKey(ctx, ceremonyID)
		if err != nil {
			return nil, fmt.Errorf("create HSM import key: %w", err)
		}
		break
	}
	if ceremonyID == "" || len(wrappingPublicKey) != 32 {
		return nil, fmt.Errorf("%w: HSM import public key must be X25519", ErrInvalidArgument)
	}

	wrappingDigest := sha256.Sum256(wrappingPublicKey)
	binding := CeremonyBinding{
		CeremonyID:            ceremonyID,
		Slot:                  spec.Slot,
		Algorithm:             spec.Algorithm,
		Purpose:               spec.Purpose,
		KeyMaterialCommitment: spec.KeyMaterialCommitment,
		WrappingKeyDigest:     wrappingDigest,
		StartUnixNano:         now.UnixNano(),
		DeadlineUnixNano:      deadline.UnixNano(),
		PolicyVersion:         policy.version,
	}
	ceremony := &Ceremony{
		host:              h,
		binding:           binding,
		wrappingPublicKey: append([]byte(nil), wrappingPublicKey...),
		start:             now,
		deadline:          deadline,
		state:             StateCollecting,
		approvals:         make(map[string]approvalRecord),
		shares:            make(map[string]shareRecord),
	}
	h.mu.Lock()
	if _, exists := h.ceremonies[ceremonyID]; exists {
		h.mu.Unlock()
		return nil, fmt.Errorf("%w: ceremony ID collision", ErrInvalidArgument)
	}
	h.ceremonies[ceremonyID] = ceremony
	h.mu.Unlock()
	return ceremony, nil
}

// CeremonyByID returns a live ceremony handle.
func (h *Host) CeremonyByID(ceremonyID string) (*Ceremony, error) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	ceremony, ok := h.ceremonies[ceremonyID]
	if !ok {
		return nil, ErrCeremonyNotFound
	}
	return ceremony, nil
}

// Binding returns an immutable copy of the ceremony binding.
func (c *Ceremony) Binding() CeremonyBinding {
	return c.binding
}

// WrappingPublicKey returns the one-time X25519 public key that participants
// use outside the host to seal their individual shares.
func (c *Ceremony) WrappingPublicKey() []byte {
	return append([]byte(nil), c.wrappingPublicKey...)
}

// SealedShare is an authenticated, HSM-recipient-encrypted 32-byte share.
type SealedShare struct {
	CeremonyID         string
	PersonID           string
	CredentialID       string
	EphemeralPublicKey []byte
	Nonce              []byte
	Ciphertext         []byte
	Signature          []byte
}

func (s SealedShare) clone() SealedShare {
	s.EphemeralPublicKey = append([]byte(nil), s.EphemeralPublicKey...)
	s.Nonce = append([]byte(nil), s.Nonce...)
	s.Ciphertext = append([]byte(nil), s.Ciphertext...)
	s.Signature = append([]byte(nil), s.Signature...)
	return s
}

func (s SealedShare) wrapped() WrappedShare {
	return WrappedShare{
		PersonID:           s.PersonID,
		CredentialID:       s.CredentialID,
		EphemeralPublicKey: append([]byte(nil), s.EphemeralPublicKey...),
		Nonce:              append([]byte(nil), s.Nonce...),
		Ciphertext:         append([]byte(nil), s.Ciphertext...),
	}
}

// EnvelopeSigningPayload is the credential signature over a sealed share.
func EnvelopeSigningPayload(envelope SealedShare) []byte {
	payload := canonicalField(nil, []byte("netsec.import-share-envelope.v1"))
	payload = canonicalString(payload, envelope.CeremonyID)
	payload = canonicalString(payload, envelope.PersonID)
	payload = canonicalString(payload, envelope.CredentialID)
	payload = canonicalBytes(payload, envelope.EphemeralPublicKey)
	payload = canonicalBytes(payload, envelope.Nonce)
	payload = canonicalBytes(payload, envelope.Ciphertext)
	return payload
}

// GenerateShare returns a cryptographically random 32-byte XOR share.
func GenerateShare() ([32]byte, error) {
	var share [32]byte
	_, err := rand.Read(share[:])
	return share, err
}

// SealShare seals a share to the one-time HSM import key and authenticates
// the envelope with the administrator's Ed25519 credential.
func SealShare(binding CeremonyBinding, wrappingPublicKey []byte, credentialID, personID string, privateKey ed25519.PrivateKey, share [32]byte) (*SealedShare, error) {
	return sealShare(binding, wrappingPublicKey, credentialID, personID, privateKey, share)
}

// Approve records one valid vote. Multiple credentials for one person are
// rejected after the first vote, regardless of which credential was used.
func (c *Ceremony) Approve(ctx context.Context, approval Approval) error {
	if ctx == nil {
		return ErrInvalidArgument
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.refreshLocked(); err != nil {
		return err
	}
	if c.state != StateCollecting {
		return ErrCeremonyClosed
	}
	if approval.Decision != DecisionApprove {
		return ErrInvalidApproval
	}
	credential, ok := c.host.registry.Credential(approval.CredentialID)
	if !ok {
		return ErrCredentialNotFound
	}
	if credential.Revoked {
		return ErrCredentialRevoked
	}
	policy := c.host.registry.policySnapshot()
	if !isPolicyRoleAllowed(policy, credential.Role) {
		return ErrApprovalRoleNotAllowed
	}
	payload := ApprovalSigningPayload(c.binding, approval.CredentialID, credential.PersonID, approval.Decision)
	if !ed25519.Verify(credential.PublicKey, payload, approval.Signature) {
		return ErrInvalidSignature
	}
	if existing, ok := c.approvals[credential.PersonID]; ok {
		if existing.credentialID != approval.CredentialID {
			return ErrDuplicatePerson
		}
		return nil
	}
	c.approvals[credential.PersonID] = approvalRecord{
		credentialID:         approval.CredentialID,
		personID:             credential.PersonID,
		role:                 credential.Role,
		revocationGeneration: credential.Credential.RevocationGeneration,
	}
	return nil
}

// SubmitShare verifies the participant credential signature and binds the
// envelope to this ceremony, person, and credential. No plaintext is accepted.
func (c *Ceremony) SubmitShare(ctx context.Context, share SealedShare) error {
	if ctx == nil {
		return ErrInvalidArgument
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.refreshLocked(); err != nil {
		return err
	}
	if c.state != StateCollecting {
		return ErrCeremonyClosed
	}
	if share.CeremonyID != c.binding.CeremonyID {
		return ErrEnvelopeMismatch
	}
	if len(share.EphemeralPublicKey) != 32 || len(share.Nonce) != 12 || len(share.Ciphertext) != 48 {
		return ErrInvalidArgument
	}
	credential, ok := c.host.registry.Credential(share.CredentialID)
	if !ok {
		return ErrCredentialNotFound
	}
	if credential.Revoked {
		return ErrCredentialRevoked
	}
	if credential.PersonID != share.PersonID {
		return ErrEnvelopeMismatch
	}
	policy := c.host.registry.policySnapshot()
	if !isPolicyRoleAllowed(policy, credential.Role) {
		return ErrApprovalRoleNotAllowed
	}
	if !ed25519.Verify(credential.PublicKey, EnvelopeSigningPayload(share), share.Signature) {
		return ErrInvalidSignature
	}
	if existing, ok := c.shares[share.PersonID]; ok {
		if !sealedSharesEqual(existing.envelope, share) {
			return ErrDuplicateShare
		}
		return nil
	}
	c.shares[share.PersonID] = shareRecord{
		personID:             share.PersonID,
		credentialID:         share.CredentialID,
		role:                 credential.Role,
		revocationGeneration: credential.Credential.RevocationGeneration,
		envelope:             share.clone(),
	}
	return nil
}

func sealedSharesEqual(a, b SealedShare) bool {
	return a.CeremonyID == b.CeremonyID &&
		a.PersonID == b.PersonID &&
		a.CredentialID == b.CredentialID &&
		bytes.Equal(a.EphemeralPublicKey, b.EphemeralPublicKey) &&
		bytes.Equal(a.Nonce, b.Nonce) &&
		bytes.Equal(a.Ciphertext, b.Ciphertext) &&
		bytes.Equal(a.Signature, b.Signature)
}

// Complete atomically dispatches at most one HSM import for this ceremony.
// Every unique approving person must also have submitted exactly one share.
func (c *Ceremony) Complete(ctx context.Context) (*ImportResult, error) {
	if ctx == nil {
		return nil, ErrInvalidArgument
	}
	for {
		c.mu.Lock()
		if err := c.refreshLocked(); err != nil {
			c.mu.Unlock()
			return nil, err
		}
		switch c.state {
		case StateCompleted:
			result := cloneImportResult(c.result)
			c.mu.Unlock()
			return result, nil
		case StateFailed:
			reason := c.reason
			c.mu.Unlock()
			return nil, reason
		case StateImporting:
			done := c.importDone
			c.mu.Unlock()
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-done:
				continue
			}
		}
		if err := c.validateReadyLocked(); err != nil {
			c.mu.Unlock()
			return nil, err
		}
		wrappedShares, importCredentials, err := c.participantSharesLocked()
		if err != nil {
			c.mu.Unlock()
			return nil, err
		}
		c.state = StateImporting
		c.importAttempted = true
		c.importCredentials = importCredentials
		c.importDone = make(chan struct{})
		c.importClosed = false
		binding := c.binding
		c.mu.Unlock()

		result, importErr := c.host.hsm.CombineAndImport(ctx, ImportRequest{
			Binding:       binding,
			WrappedShares: wrappedShares,
		})
		if importErr == nil {
			if err := c.validateImportResult(result); err == nil {
				if err := c.finishImport(&result, nil); err != nil {
					return nil, err
				}
				return cloneImportResult(&result), nil
			} else {
				importErr = err
			}
		}

		readResult, readErr := c.host.hsm.ReadReceipt(context.WithoutCancel(ctx), binding.CeremonyID)
		if readErr == nil {
			if err := c.validateImportResult(readResult); err == nil {
				if err := c.finishImport(&readResult, nil); err != nil {
					return nil, err
				}
				return cloneImportResult(&readResult), nil
			}
		}

		uncertain := fmt.Errorf("%w: %v", ErrImportUncertain, importErr)
		if finishErr := c.finishImport(nil, uncertain); finishErr != nil &&
			!errors.Is(finishErr, ErrImportUncertain) {
			return nil, finishErr
		}
		return nil, uncertain
	}
}

func (c *Ceremony) validateReadyLocked() error {
	policy := c.host.registry.policySnapshot()
	if len(c.approvals) < policy.threshold {
		return ErrNotReady
	}
	seenRoles := make(map[Role]struct{})
	participants := 0
	for personID, approval := range c.approvals {
		if _, ok := c.shares[personID]; !ok {
			return fmt.Errorf("%w: approving person %q has not contributed a share", ErrNotReady, personID)
		}
		participants++
		seenRoles[approval.role] = struct{}{}
	}
	if participants < policy.shareThreshold {
		return ErrNotReady
	}
	if missingRole(policy, seenRoles) {
		return ErrMissingRequiredRole
	}
	return nil
}

func (c *Ceremony) participantSharesLocked() ([]WrappedShare, []credentialRef, error) {
	personIDs := make([]string, 0, len(c.approvals))
	for personID := range c.approvals {
		personIDs = append(personIDs, personID)
	}
	sort.Strings(personIDs)
	wrapped := make([]WrappedShare, 0, len(personIDs))
	credentials := make([]credentialRef, 0, len(personIDs)*2)
	for _, personID := range personIDs {
		record, ok := c.shares[personID]
		if !ok {
			return nil, nil, fmt.Errorf("%w: no share for person %q", ErrNotReady, personID)
		}
		approval := c.approvals[personID]
		credentials = append(credentials, credentialRef{
			credentialID:         approval.credentialID,
			personID:             approval.personID,
			role:                 approval.role,
			revocationGeneration: approval.revocationGeneration,
		}, credentialRef{
			credentialID:         record.credentialID,
			personID:             record.personID,
			role:                 record.role,
			revocationGeneration: record.revocationGeneration,
		})
		wrapped = append(wrapped, record.envelope.wrapped())
	}
	return wrapped, credentials, nil
}

func (c *Ceremony) validateImportResult(result ImportResult) error {
	expectedAttributes := AttributesDigest(c.binding)
	if result.KeyHandle == "" {
		return fmt.Errorf("%w: empty key handle", ErrImportMismatch)
	}
	if result.AttributesDigest != expectedAttributes {
		return fmt.Errorf("%w: attribute digest mismatch", ErrImportMismatch)
	}
	if result.KeyMaterialCommitment != c.binding.KeyMaterialCommitment {
		return fmt.Errorf("%w: key material commitment mismatch", ErrImportMismatch)
	}
	if err := verifyReceipt(c.binding, result, c.host.receiptKey); err != nil {
		return err
	}
	return nil
}

func (c *Ceremony) finishImport(result *ImportResult, importErr error) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.state == StateCompleted {
		return nil
	}
	if c.state == StateFailed {
		return c.reason
	}
	if c.state != StateImporting {
		return ErrCeremonyClosed
	}
	if importErr != nil {
		c.recoverableReceipt = true
		c.terminateLocked(importErr)
		return c.reason
	}
	if err := c.refreshLocked(); err != nil {
		return err
	}
	c.result = cloneImportResult(result)
	c.state = StateCompleted
	c.reason = nil
	c.importCredentials = nil
	c.destroySharesLocked()
	c.closeImportWaitersLocked()
	return nil
}

func (c *Ceremony) terminateLocked(reason error) {
	if c.state == StateCompleted || c.state == StateFailed {
		return
	}
	c.state = StateFailed
	c.reason = reason
	c.destroySharesLocked()
	c.closeImportWaitersLocked()
}

func (c *Ceremony) closeImportWaitersLocked() {
	if c.importDone != nil && !c.importClosed {
		close(c.importDone)
		c.importClosed = true
	}
}

func (c *Ceremony) destroySharesLocked() {
	for personID, record := range c.shares {
		zeroBytes(record.envelope.EphemeralPublicKey)
		zeroBytes(record.envelope.Nonce)
		zeroBytes(record.envelope.Ciphertext)
		zeroBytes(record.envelope.Signature)
		delete(c.shares, personID)
	}
	c.shares = make(map[string]shareRecord)
}

func zeroBytes(value []byte) {
	for i := range value {
		value[i] = 0
	}
}

func (c *Ceremony) refreshLocked() error {
	if c.state == StateCompleted {
		return c.reason
	}
	if c.state == StateFailed {
		if c.recoverableReceipt {
			return c.validateRecoveryLocked()
		}
		return c.reason
	}
	if !c.host.clock.Now().Before(c.deadline) {
		c.terminateLocked(ErrCeremonyExpired)
		return c.reason
	}
	policy := c.host.registry.policySnapshot()
	if policy.version != c.binding.PolicyVersion {
		c.terminateLocked(ErrPolicyChanged)
		return c.reason
	}
	for _, approval := range c.approvals {
		current, ok := c.host.registry.Credential(approval.credentialID)
		if !ok || current.Revoked || current.Credential.RevocationGeneration != approval.revocationGeneration {
			c.terminateLocked(ErrCredentialRevoked)
			return c.reason
		}
		if current.Credential.PersonID != approval.personID ||
			current.Credential.Role != approval.role ||
			!isPolicyRoleAllowed(policy, current.Credential.Role) {
			c.terminateLocked(ErrCredentialRevoked)
			return c.reason
		}
	}
	for _, share := range c.shares {
		current, ok := c.host.registry.Credential(share.credentialID)
		if !ok || current.Revoked || current.Credential.RevocationGeneration != share.revocationGeneration ||
			current.Credential.PersonID != share.personID {
			c.terminateLocked(ErrCredentialRevoked)
			return c.reason
		}
	}
	return nil
}

func (c *Ceremony) validateRecoveryLocked() error {
	if !c.host.clock.Now().Before(c.deadline) {
		c.reason = ErrCeremonyExpired
		c.recoverableReceipt = false
		c.destroySharesLocked()
		return c.reason
	}
	policy := c.host.registry.policySnapshot()
	if policy.version != c.binding.PolicyVersion {
		c.reason = ErrPolicyChanged
		c.recoverableReceipt = false
		c.destroySharesLocked()
		return c.reason
	}
	for _, ref := range c.importCredentials {
		current, ok := c.host.registry.Credential(ref.credentialID)
		if !ok || current.Revoked ||
			current.Credential.RevocationGeneration != ref.revocationGeneration ||
			current.Credential.PersonID != ref.personID ||
			current.Credential.Role != ref.role ||
			!isPolicyRoleAllowed(policy, current.Credential.Role) {
			c.reason = ErrCredentialRevoked
			c.recoverableReceipt = false
			c.destroySharesLocked()
			return c.reason
		}
	}
	return nil
}

// Status returns counters and the terminal reason, if any.
func (c *Ceremony) Status() CeremonyStatus {
	c.mu.Lock()
	defer c.mu.Unlock()
	_ = c.refreshLocked()
	policy := c.host.registry.policySnapshot()
	status := CeremonyStatus{
		CeremonyID:        c.binding.CeremonyID,
		State:             c.state,
		ApprovalCount:     len(c.approvals),
		ShareCount:        len(c.shares),
		RequiredApprovals: policy.threshold,
		RequiredShares:    policy.shareThreshold,
		Start:             c.start,
		Deadline:          c.deadline,
	}
	if c.reason != nil {
		status.Reason = c.reason.Error()
	}
	return status
}

// ReadReceipt reconciles a previously dispatched import by ceremony ID. It is
// the recovery path for a lost CombineAndImport response.
func (c *Ceremony) ReadReceipt(ctx context.Context) (*ImportResult, error) {
	if ctx == nil {
		return nil, ErrInvalidArgument
	}
	for {
		c.mu.Lock()
		if c.state == StateCompleted {
			result := cloneImportResult(c.result)
			c.mu.Unlock()
			return result, nil
		}
		if c.state == StateCollecting && !c.importAttempted {
			c.mu.Unlock()
			return nil, ErrReceiptNotFound
		}
		if c.state == StateFailed && !c.recoverableReceipt {
			reason := c.reason
			c.mu.Unlock()
			return nil, reason
		}
		if c.state == StateImporting {
			done := c.importDone
			c.mu.Unlock()
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-done:
				continue
			}
		}
		if c.recoverableReceipt {
			if err := c.validateRecoveryLocked(); err != nil {
				c.mu.Unlock()
				return nil, err
			}
		}
		binding := c.binding
		c.mu.Unlock()

		result, err := c.host.hsm.ReadReceipt(ctx, binding.CeremonyID)
		if err != nil {
			return nil, fmt.Errorf("%w: %v", ErrReceiptNotFound, err)
		}
		if err := c.validateImportResult(result); err != nil {
			return nil, err
		}

		c.mu.Lock()
		if c.recoverableReceipt {
			if err := c.validateRecoveryLocked(); err != nil {
				c.mu.Unlock()
				return nil, err
			}
		}
		if c.state == StateFailed && c.recoverableReceipt {
			c.result = cloneImportResult(&result)
			c.state = StateCompleted
			c.reason = nil
			c.recoverableReceipt = false
			c.importCredentials = nil
			c.destroySharesLocked()
			c.closeImportWaitersLocked()
		}
		if c.state == StateCompleted {
			resultCopy := cloneImportResult(c.result)
			c.mu.Unlock()
			return resultCopy, nil
		}
		reason := c.reason
		c.mu.Unlock()
		return nil, reason
	}
}

// Approve is the host-addressed form of Ceremony.Approve.
func (h *Host) Approve(ctx context.Context, ceremonyID string, approval Approval) error {
	ceremony, err := h.CeremonyByID(ceremonyID)
	if err != nil {
		return err
	}
	return ceremony.Approve(ctx, approval)
}

// SubmitShare is the host-addressed form of Ceremony.SubmitShare.
func (h *Host) SubmitShare(ctx context.Context, ceremonyID string, share SealedShare) error {
	ceremony, err := h.CeremonyByID(ceremonyID)
	if err != nil {
		return err
	}
	return ceremony.SubmitShare(ctx, share)
}

// Complete is the host-addressed form of Ceremony.Complete.
func (h *Host) Complete(ctx context.Context, ceremonyID string) (*ImportResult, error) {
	ceremony, err := h.CeremonyByID(ceremonyID)
	if err != nil {
		return nil, err
	}
	return ceremony.Complete(ctx)
}

// ReadReceipt is the host-addressed form of Ceremony.ReadReceipt.
func (h *Host) ReadReceipt(ctx context.Context, ceremonyID string) (*ImportResult, error) {
	ceremony, err := h.CeremonyByID(ceremonyID)
	if err != nil {
		return nil, err
	}
	return ceremony.ReadReceipt(ctx)
}

// Status is the host-addressed form of Ceremony.Status.
func (h *Host) Status(ceremonyID string) (CeremonyStatus, error) {
	ceremony, err := h.CeremonyByID(ceremonyID)
	if err != nil {
		return CeremonyStatus{}, err
	}
	return ceremony.Status(), nil
}

func (h *Host) registryChanged() {
	h.mu.RLock()
	ceremonies := make([]*Ceremony, 0, len(h.ceremonies))
	for _, ceremony := range h.ceremonies {
		ceremonies = append(ceremonies, ceremony)
	}
	h.mu.RUnlock()
	for _, ceremony := range ceremonies {
		ceremony.mu.Lock()
		_ = ceremony.refreshLocked()
		ceremony.mu.Unlock()
	}
}

func cloneImportResult(result *ImportResult) *ImportResult {
	if result == nil {
		return nil
	}
	copy := *result
	copy.Receipt.Signature = append([]byte(nil), result.Receipt.Signature...)
	return &copy
}
