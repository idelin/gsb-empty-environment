// Package netsec verifies authenticated algorithm-negotiation transcripts.
//
// A negotiation transcript is an ordered sequence of four records:
//
//  0. party A capability list
//  1. party B capability list
//  2. algorithm selection, made by party B
//  3. mutual confirmation signed by both parties
//
// Every record carries its exact wire bytes (Raw) together with one or more
// authentication marks (Ed25519 signatures). The verifier never trusts a
// parsed representation: every security decision is derived from records whose
// raw bytes authenticate under trusted public keys. Capabilities that only
// appear in records which fail authentication are returned as unverified
// clues and can never establish that an algorithm was actually offered or
// selected.
package netsec

import (
	"crypto/ed25519"
	"errors"
)

// Party identifies one of the two negotiating identities.
type Party uint8

const (
	// PartyUnknown is returned for malformed marks.
	PartyUnknown Party = 0
	// PartyA is the initiator.
	PartyA Party = 1
	// PartyB is the responder and the party that makes the selection.
	PartyB Party = 2
)

// RecordType identifies the role of a record in the transcript.
type RecordType uint8

const (
	// TypeCapabilitiesA is party A's authenticated capability offer.
	TypeCapabilitiesA RecordType = 1
	// TypeCapabilitiesB is party B's authenticated capability offer.
	TypeCapabilitiesB RecordType = 2
	// TypeSelection is party B's authenticated algorithm choice.
	TypeSelection RecordType = 3
	// TypeConfirmation is the mutually signed final confirmation.
	TypeConfirmation RecordType = 4
)

// Mark is an authentication marker attached to a record: an Ed25519 signature
// produced by Signer over the record's canonical signing payload (see
// signingPayload). The signature covers the exact Raw bytes, the session
// identifier and the record position in the transcript.
type Mark struct {
	Signer    Party
	Signature []byte
}

// Record is one transcript entry as observed on the wire. Raw is immutable
// evidence and must be the exact byte string that was authenticated; Marks are
// the authentication markers claimed for it.
type Record struct {
	Type  RecordType
	Raw   []byte
	Marks []Mark
}

// Severity classifies a Finding.
type Severity uint8

const (
	// SevInfo is informational and never affects the verdict.
	SevInfo Severity = 1
	// SevWarning indicates suspicious but non-fatal evidence.
	SevWarning Severity = 2
	// SevError invalidates the Authentic verdict.
	SevError Severity = 3
)

// FindingCode is a stable, machine-readable identifier for a Finding.
type FindingCode string

const (
	// FindingMalformed means raw bytes cannot be parsed canonically.
	FindingMalformed FindingCode = "malformed-record"
	// FindingMissingRecord means an expected transcript slot is absent.
	FindingMissingRecord FindingCode = "missing-record"
	// FindingUnexpectedRecord means the transcript contains extra records.
	FindingUnexpectedRecord FindingCode = "unexpected-record"
	// FindingReordered means a record appears at the wrong transcript position.
	FindingReordered FindingCode = "reordered-record"
	// FindingBadSignature means an authentication mark failed verification,
	// including marks over deleted, reordered or foreign-session bytes.
	FindingBadSignature FindingCode = "bad-signature"
	// FindingMissingSignature means a required signer produced no mark.
	FindingMissingSignature FindingCode = "missing-signature"
	// FindingUnknownSigner means a mark refers to an untrusted identity.
	FindingUnknownSigner FindingCode = "unknown-signer"
	// FindingDuplicateAlgorithm means a capability list repeats an algorithm.
	FindingDuplicateAlgorithm FindingCode = "duplicate-algorithm"
	// FindingSelectionNotInIntersection means the chosen algorithm is not in
	// the authenticated capability intersection.
	FindingSelectionNotInIntersection FindingCode = "selection-not-in-intersection"
	// FindingPreferenceViolation means a lower-preference algorithm was chosen.
	FindingPreferenceViolation FindingCode = "preference-violation"
	// FindingHashMismatch means the selection does not bind the observed lists.
	FindingHashMismatch FindingCode = "selection-hash-mismatch"
	// FindingChainMismatch means the confirmation does not bind the transcript.
	FindingChainMismatch FindingCode = "confirmation-chain-mismatch"
	// FindingWeakSelection means the authenticated choice fails host policy.
	FindingWeakSelection FindingCode = "weak-selection"
)

// Finding is one structured verification observation.
type Finding struct {
	Severity    Severity
	Code        FindingCode
	RecordIndex int
	Message     string
}

// CapabilityEvidence is one observed algorithm with its provenance.
// Capabilities with Authenticated == false are clues only and must never be
// treated as proof of what a party could offer.
type CapabilityEvidence struct {
	Algorithm     string
	Party         Party
	RecordIndex   int
	Raw           []byte
	Authenticated bool
}

// RecordEvidence exposes the raw bytes and authentication state of one record.
type RecordEvidence struct {
	Index         int
	Type          RecordType
	Raw           []byte
	Marks         []Mark
	Authenticated bool
	Algorithms    []string
	Selected      string
}

// Policy configures a Verifier with the trust anchor and negotiation rules of
// the verification host.
type Policy struct {
	// SessionID must match the session bound into every signature.
	SessionID []byte
	// PartyAKey and PartyBKey are the trusted Ed25519 public keys.
	PartyAKey ed25519.PublicKey
	PartyBKey ed25519.PublicKey
	// PreferenceOrder is the chooser's input preference order. When nil, the
	// authenticated order of party B's capability list is used.
	PreferenceOrder []string
	// Acceptable is the host security policy. When non-nil, a selected
	// algorithm outside the set yields FindingWeakSelection.
	Acceptable map[string]bool
}

// Report is the verification result.
type Report struct {
	// Authentic is true only when every record is well-formed, in order, and
	// authenticated by exactly the required trusted signers for this session.
	Authentic bool
	// Secure is true only when Authentic and, when Policy.Acceptable is set,
	// the selected algorithm satisfies the host security policy.
	Secure bool

	SessionID []byte

	Records                    []RecordEvidence
	AuthenticatedCapabilitiesA []string
	AuthenticatedCapabilitiesB []string
	Intersection               []string
	Selected                   string
	SelectedAuthenticated      bool
	Clues                      []CapabilityEvidence
	Findings                   []Finding
}

// Verifier checks negotiation transcripts against a fixed Policy.
type Verifier struct {
	policy Policy
}

// New validates the policy trust anchors and returns a Verifier.
func New(policy Policy) (*Verifier, error) {
	if len(policy.PartyAKey) != ed25519.PublicKeySize {
		return nil, errors.New("netsec: party A public key has wrong size")
	}
	if len(policy.PartyBKey) != ed25519.PublicKeySize {
		return nil, errors.New("netsec: party B public key has wrong size")
	}
	return &Verifier{policy: policy}, nil
}
