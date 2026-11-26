// Package credentialauth implements offline merge and global consumption
// proofs for credentials shared by a small set of security-key devices.
//
// The package deliberately works on already-verified, signed receipt data.
// Signature verification is a responsibility of the importing host, which
// knows the device public keys and trust policy.  This package binds the
// supplied receipt contents into the proof digest and enforces the state and
// consumption invariants that can be checked from those contents alone.
package credentialauth

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// MaxDevices is the supported number of independent offline devices.
const MaxDevices = 3

// ProofVersion identifies the canonical proof format emitted by this package.
const ProofVersion = 1

// SafetyMode selects how a permit candidate is checked against the ambiguous
// batch summaries.
//
// A fixed-completion proof commits to one deterministic completion of the
// incomplete batch log.  A candidate is acceptable when at least one
// completion exists in which the candidate is not consumed.
//
// A universal proof is stricter: a candidate is acceptable only when it is
// absent from every feasible completion of the batch log.  This is useful when
// the host cannot rely on the proof itself becoming the authoritative
// completion.
type SafetyMode string

const (
	// SafetyFixedCompletion is the default mode.  The proof fixes a complete
	// assignment and therefore resolves the batch log's missing
	// credential-to-transaction correspondence.
	SafetyFixedCompletion SafetyMode = "fixed-completion"
	// SafetyUniversal requires the candidate to be unused in every compatible
	// completion.
	SafetyUniversal SafetyMode = "universal"
)

// CredentialRef identifies a permanent WebAuthn credential.
//
// RPID and CredentialID together form the credential's identity.  Credential
// IDs are intentionally not global: two RPs may issue the same byte string,
// and they must remain isolated.
type CredentialRef struct {
	RPID         string   `json:"rpID"`
	CredentialID string   `json:"credentialID,omitempty"`
	ID           string   `json:"id,omitempty"`
	Purposes     []string `json:"purposes,omitempty"`
	// AllowedPurposes is accepted as a synonym for Purposes.
	AllowedPurposes []string `json:"allowedPurposes,omitempty"`
	// SourceDeviceID is informational.  It does not become part of credential
	// identity because the same user credential can be replicated to multiple
	// devices.
	SourceDeviceID string `json:"sourceDeviceID,omitempty"`
}

// Credential is an alias kept for callers that prefer the shorter name.
type Credential = CredentialRef

// CredentialIDValue returns the effective permanent credential ID.
func (c CredentialRef) CredentialIDValue() string {
	if c.CredentialID != "" {
		return c.CredentialID
	}
	return c.ID
}

// RPIDValue returns the effective relying-party identifier.
func (c CredentialRef) RPIDValue() string {
	return c.RPID
}

// DeviceReceipt is one independently supplied signed device snapshot.
//
// A receipt may describe one credential using the top-level RPID,
// CredentialID, AllowedPurposes and TransactionDigest fields, or several
// credentials using Credentials.  The top-level fields are merged with the
// nested records when both are present.
type DeviceReceipt struct {
	ReceiptID        string `json:"receiptID,omitempty"`
	DeviceID         string `json:"deviceID"`
	BootGeneration   uint64 `json:"bootGeneration"`
	SignatureCounter uint64 `json:"signatureCounter"`

	RPID              string          `json:"rpID,omitempty"`
	CredentialID      string          `json:"credentialID,omitempty"`
	AllowedPurposes   []string        `json:"allowedPurposes,omitempty"`
	TransactionDigest string          `json:"transactionDigest,omitempty"`
	Credentials       []CredentialRef `json:"credentials,omitempty"`
	Signature         string          `json:"signature,omitempty"`
}

// Receipt is a convenient alias for DeviceReceipt.
type Receipt = DeviceReceipt

// BatchSummary is the surviving aggregate form of a confirmed batch.
//
// TransactionCount transactions in the batch must be assigned distinct
// credentials from CandidateCredentials.  The original one-to-one
// correspondence was truncated and is intentionally absent.
type BatchSummary struct {
	BatchID string `json:"batchID,omitempty"`
	ID      string `json:"id,omitempty"`

	RPID                 string          `json:"rpID,omitempty"`
	CandidateCredentials []CredentialRef `json:"candidateCredentials,omitempty"`
	Candidates           []CredentialRef `json:"candidates,omitempty"`

	TransactionCount   int      `json:"transactionCount"`
	Count              int      `json:"count,omitempty"`
	TransactionDigests []string `json:"transactionDigests,omitempty"`

	RequiredPurpose string `json:"requiredPurpose,omitempty"`
	Purpose         string `json:"purpose,omitempty"`
}

// Batch is a convenient alias for BatchSummary.
type Batch = BatchSummary

// Reservation is a credential that was reserved by an unconfirmed transaction
// before revocation.  It remains locked and may not satisfy a confirmed
// transaction or a new permit.
type Reservation struct {
	RPID         string `json:"rpID"`
	CredentialID string `json:"credentialID,omitempty"`
	ID           string `json:"id,omitempty"`
	DeviceID     string `json:"deviceID,omitempty"`
	Reason       string `json:"reason,omitempty"`
}

// ReservedCredential is an alias for Reservation.
type ReservedCredential = Reservation

// PermitRequest asks the verifier to lock one credential for a new
// transaction.  If Candidates is empty, all source credentials under RPID are
// considered.
type PermitRequest struct {
	RPID              string          `json:"rpID"`
	TransactionDigest string          `json:"transactionDigest,omitempty"`
	Purpose           string          `json:"purpose,omitempty"`
	RequiredPurpose   string          `json:"requiredPurpose,omitempty"`
	Candidates        []CredentialRef `json:"candidates,omitempty"`
	CredentialIDs     []string        `json:"credentialIDs,omitempty"`
	CredentialID      string          `json:"credentialID,omitempty"`
	Mode              SafetyMode      `json:"mode,omitempty"`
	RequireUniversal  bool            `json:"requireUniversal,omitempty"`
}

// Request is an alias for PermitRequest.
type Request = PermitRequest

// Snapshot contains the complete offline merge input.
//
// Receipts/Devices and Batches/ConfirmedBatches are accepted as aliases so
// that JSON producers can use either spelling.  Request/Pending are likewise
// aliases; at most one effective request is allowed.
type Snapshot struct {
	Devices          []DeviceReceipt `json:"devices,omitempty"`
	Receipts         []DeviceReceipt `json:"receipts,omitempty"`
	Batches          []BatchSummary  `json:"batches,omitempty"`
	ConfirmedBatches []BatchSummary  `json:"confirmedBatches,omitempty"`
	Reservations     []Reservation   `json:"reservations,omitempty"`
	Reserved         []Reservation   `json:"reserved,omitempty"`
	Request          *PermitRequest  `json:"request,omitempty"`
	Pending          *PermitRequest  `json:"pending,omitempty"`
}

// MergeInput is an alias for Snapshot.
type MergeInput = Snapshot

// Input is an alias for Snapshot.
type Input = Snapshot

// BatchAssignment is one fixed credential allocation for a batch.
type BatchAssignment struct {
	BatchID            string          `json:"batchID"`
	TransactionCount   int             `json:"transactionCount"`
	TransactionDigests []string        `json:"transactionDigests,omitempty"`
	Credentials        []CredentialRef `json:"credentials"`
}

// CredentialStatus records how a credential behaves across all completions.
//
// ConditionallyUsed is true when at least one compatible completion can
// consume the credential.  Unavoidable is true when every compatible
// completion consumes it.  UniversallyFree is the complement of
// ConditionallyUsed.  Reserved credentials are never available even when
// UniversallyFree is true.
type CredentialStatus struct {
	Credential        CredentialRef `json:"credential"`
	Reserved          bool          `json:"reserved"`
	Consumed          bool          `json:"consumed"`
	ConditionallyUsed bool          `json:"conditionallyUsed"`
	Unavoidable       bool          `json:"unavoidable"`
	UniversallyFree   bool          `json:"universallyFree"`
	Available         bool          `json:"available"`
}

// Proof is a deterministic, fixed global consumption proof.
type Proof struct {
	Version           int                `json:"version"`
	Digest            string             `json:"digest"`
	SnapshotDigest    string             `json:"snapshotDigest"`
	RequestDigest     string             `json:"requestDigest,omitempty"`
	Safety            SafetyMode         `json:"safety,omitempty"`
	Request           *PermitRequest     `json:"request,omitempty"`
	Assignment        []BatchAssignment  `json:"assignment"`
	AssignmentDigest  string             `json:"assignmentDigest"`
	Consumed          []CredentialRef    `json:"consumed"`
	Reserved          []CredentialRef    `json:"reserved"`
	Locked            []CredentialRef    `json:"locked"`
	Unavoidable       []CredentialRef    `json:"unavoidable"`
	ConditionallyUsed []CredentialRef    `json:"conditionallyUsed"`
	UniversallyFree   []CredentialRef    `json:"universallyFree"`
	Available         []CredentialRef    `json:"available"`
	Statuses          []CredentialStatus `json:"statuses"`
	Permit            *Permit            `json:"permit,omitempty"`
}

// ConsumptionProof is an alias for Proof.
type ConsumptionProof = Proof

// LockProof is an alias for Proof.
type LockProof = Proof

// Permit is the fixed, single-consumption lock issued with a proof.
type Permit struct {
	LockID            string        `json:"lockID"`
	SnapshotDigest    string        `json:"snapshotDigest"`
	RequestDigest     string        `json:"requestDigest"`
	AssignmentDigest  string        `json:"assignmentDigest"`
	RPID              string        `json:"rpID"`
	Credential        CredentialRef `json:"credential"`
	TransactionDigest string        `json:"transactionDigest,omitempty"`
	Purpose           string        `json:"purpose,omitempty"`
}

// ConsumptionPermit is an alias for Permit.
type ConsumptionPermit = Permit

type credentialKey struct {
	rpID string
	id   string
}

func (k credentialKey) String() string {
	return k.rpID + "\x00" + k.id
}

func (k credentialKey) less(other credentialKey) bool {
	if k.rpID != other.rpID {
		return k.rpID < other.rpID
	}
	return k.id < other.id
}

func (k credentialKey) ref() CredentialRef {
	return CredentialRef{RPID: k.rpID, CredentialID: k.id}
}

type purposeSet struct {
	all bool
	set map[string]struct{}
}

func newPurposeSet(values ...[]string) purposeSet {
	var nonempty []string
	for _, group := range values {
		if len(group) > 0 {
			nonempty = append(nonempty, group...)
		}
	}
	if len(nonempty) == 0 {
		return purposeSet{all: true}
	}
	result := purposeSet{set: make(map[string]struct{}, len(nonempty))}
	for _, value := range nonempty {
		value = strings.TrimSpace(value)
		if value != "" {
			result.set[value] = struct{}{}
		}
	}
	if len(result.set) == 0 {
		return purposeSet{all: true}
	}
	return result
}

func (p purposeSet) allows(value string) bool {
	if value == "" || p.all {
		return true
	}
	_, ok := p.set[value]
	return ok
}

func (p purposeSet) intersect(other purposeSet) purposeSet {
	if p.all {
		return other.clone()
	}
	if other.all {
		return p.clone()
	}
	result := purposeSet{set: make(map[string]struct{})}
	for value := range p.set {
		if _, ok := other.set[value]; ok {
			result.set[value] = struct{}{}
		}
	}
	return result
}

func (p purposeSet) clone() purposeSet {
	if p.all {
		return purposeSet{all: true}
	}
	result := purposeSet{set: make(map[string]struct{}, len(p.set))}
	for value := range p.set {
		result.set[value] = struct{}{}
	}
	return result
}

func (p purposeSet) list() []string {
	if p.all {
		return nil
	}
	result := make([]string, 0, len(p.set))
	for value := range p.set {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func (p purposeSet) empty() bool {
	return !p.all && len(p.set) == 0
}

func sortedStrings(values []string) []string {
	result := append([]string(nil), values...)
	sort.Strings(result)
	return result
}

func digestBytes(parts ...[]byte) string {
	h := sha256.New()
	for _, part := range parts {
		var length [8]byte
		n := uint64(len(part))
		for i := 0; i < 8; i++ {
			length[7-i] = byte(n >> (8 * i))
		}
		_, _ = h.Write(length[:])
		_, _ = h.Write(part)
	}
	return hex.EncodeToString(h.Sum(nil))
}

func digestJSON(value any) (string, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:]), nil
}

func requestPurpose(request PermitRequest) string {
	if request.RequiredPurpose != "" {
		return request.RequiredPurpose
	}
	return request.Purpose
}

func effectiveMode(request PermitRequest) SafetyMode {
	if request.RequireUniversal || request.Mode == SafetyUniversal {
		return SafetyUniversal
	}
	return SafetyFixedCompletion
}

func (r BatchSummary) batchID() string {
	if r.BatchID != "" {
		return r.BatchID
	}
	return r.ID
}

func (r BatchSummary) transactionCount() int {
	if r.TransactionCount != 0 {
		return r.TransactionCount
	}
	return r.Count
}

func (r BatchSummary) candidates() []CredentialRef {
	result := make([]CredentialRef, 0, len(r.CandidateCredentials)+len(r.Candidates))
	result = append(result, r.CandidateCredentials...)
	result = append(result, r.Candidates...)
	return result
}

func (r BatchSummary) purpose() string {
	if r.RequiredPurpose != "" {
		return r.RequiredPurpose
	}
	return r.Purpose
}

func (r Reservation) key() (credentialKey, error) {
	id := r.CredentialID
	if id == "" {
		id = r.ID
	}
	rp := strings.TrimSpace(r.RPID)
	id = strings.TrimSpace(id)
	if rp == "" {
		return credentialKey{}, fmt.Errorf("%w: reservation has an empty rpID", ErrInvalidInput)
	}
	if id == "" {
		return credentialKey{}, fmt.Errorf("%w: reservation has an empty credential ID", ErrInvalidInput)
	}
	return credentialKey{rpID: rp, id: id}, nil
}

func (c CredentialRef) key() (credentialKey, error) {
	rp := strings.TrimSpace(c.RPID)
	id := strings.TrimSpace(c.CredentialIDValue())
	if rp == "" {
		return credentialKey{}, fmt.Errorf("%w: credential has an empty rpID", ErrInvalidInput)
	}
	if id == "" {
		return credentialKey{}, fmt.Errorf("%w: credential has an empty ID", ErrInvalidInput)
	}
	return credentialKey{rpID: rp, id: id}, nil
}

func (c CredentialRef) purposes() purposeSet {
	return newPurposeSet(c.AllowedPurposes, c.Purposes)
}

func canonicalRef(key credentialKey, purposes purposeSet, sources []string) CredentialRef {
	result := key.ref()
	result.AllowedPurposes = purposes.list()
	if len(sources) > 0 {
		sources = sortedUnique(sources)
		if len(sources) == 1 {
			result.SourceDeviceID = sources[0]
		}
	}
	return result
}

func sortedUnique(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}
