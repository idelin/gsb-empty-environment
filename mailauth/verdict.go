package mailauth

import (
	"crypto"
	"strings"
	"time"
)

// Severity ranks how strongly a finding affects the accept/reject decision.
type Severity string

const (
	SeverityInfo     Severity = "info"
	SeverityLow      Severity = "low"
	SeverityMedium   Severity = "medium"
	SeverityHigh     Severity = "high"
	SeverityCritical Severity = "critical"
)

// Finding codes are stable, machine readable identifiers emitted by the
// verifier. Hosts can map them directly into gateway policy actions.
const (
	// Message structure.
	FindingMissingFrom          = "missing_from_header"
	FindingMultipleFrom         = "multiple_from_headers"
	FindingNoDKIMSignature      = "no_dkim_signature"
	FindingMalformedSignature   = "malformed_signature"
	FindingUnsupportedAlgorithm = "unsupported_algorithm"
	FindingKeyUnavailable       = "public_key_unavailable"

	// From header coverage and oversigning.
	FindingFromNotCovered     = "from_not_covered_by_h_tag"
	FindingExtraFromUnsigned  = "extra_from_header_not_signed"
	FindingFromOversigned     = "from_oversigned_placeholder"
	FindingSignatureMismatch  = "signature_verification_failed"
	FindingBodyHashMismatch   = "body_hash_mismatch"
	FindingBodyLengthTooLarge = "body_length_tag_exceeds_body"

	// l= truncation injection and time validation.
	FindingBodyLengthInjection = "body_length_truncation_injection"
	FindingLTagSimpleCanon     = "l_tag_forbidden_with_simple_body"
	FindingSignatureExpired    = "signature_expired"
	FindingInvalidTimeRange    = "invalid_t_x_time_relationship"

	// ARC chain.
	FindingARCInstanceSequence  = "arc_instance_sequence_broken"
	FindingARCHopLimit          = "arc_hop_limit_exceeded"
	FindingARCMissingSet        = "arc_set_incomplete"
	FindingARCInitialCV         = "arc_initial_cv_not_none"
	FindingARCCVFail            = "arc_cv_fail_observed"
	FindingARCCVIllegal         = "arc_cv_value_illegal"
	FindingARCCVMismatch        = "arc_cv_does_not_match_validation"
	FindingARCMessageSigInvalid = "arc_message_signature_invalid"
	FindingARCSealInvalid       = "arc_seal_invalid"
	FindingARCUnknownField      = "arc_field_without_instance"
)

// Finding is one structured analysis observation.
type Finding struct {
	// Code is one of the Finding* constants.
	Code string
	// Severity describes the default policy weight of the finding.
	Severity Severity
	// Message is a human readable explanation.
	Message string
	// Location identifies the affected component, e.g. "message", "dkim[0]"
	// or "arc[3]".
	Location string
}

func newFinding(code string, sev Severity, location, message string) Finding {
	return Finding{Code: code, Severity: sev, Location: location, Message: message}
}

// Policy controls which findings force rejection. The zero value is not
// usable; construct policies with DefaultPolicy and adjust fields explicitly.
type Policy struct {
	// RejectMultipleFrom rejects messages carrying more than one From field.
	RejectMultipleFrom bool
	// RejectUnsignedFrom rejects when an existing From field is not covered by
	// a valid signature's h= tag.
	RejectUnsignedFrom bool
	// RejectBodyLengthInjection rejects messages whose canonical body is longer
	// than the signed l= limit, i.e. content was appended after the signed body.
	RejectBodyLengthInjection bool
	// RejectExpired rejects signatures whose x= tag is not in the future.
	RejectExpired bool
	// RejectMalformed rejects messages with no structurally valid DKIM
	// signature and no valid ARC chain.
	RejectUnauthenticated bool
	// MaxARCHops bounds the accepted ARC instance count (RFC 8617 caps at 50).
	MaxARCHops int
	// Now supplies verification time; nil uses time.Now.
	Now func() time.Time
}

// DefaultPolicy returns the strict anti-spoofing policy required by the
// package: every protective rule is enabled and the ARC hop limit is 50.
func DefaultPolicy() *Policy {
	return &Policy{
		RejectMultipleFrom:        true,
		RejectUnsignedFrom:        true,
		RejectBodyLengthInjection: true,
		RejectExpired:             true,
		RejectUnauthenticated:     true,
		MaxARCHops:                50,
		Now:                       time.Now,
	}
}

// PublicKey wraps a locally resolved DKIM/ARC public key. Key is either
// *rsa.PublicKey or ed25519.PublicKey.
type PublicKey struct {
	Key crypto.PublicKey
}

// KeyResolver supplies public keys without any network access. Hosts implement
// this on top of their own local key caches or pre-fetched records.
type KeyResolver interface {
	LookupKey(domain, selector string) (*PublicKey, bool)
}

// MemoryKeyStore is an in-process, offline KeyResolver suitable for embedded
// gateways and tests. Keys are addressed by (domain, selector).
type MemoryKeyStore struct {
	entries map[string]*PublicKey
}

// NewMemoryKeyStore creates an empty offline key store.
func NewMemoryKeyStore() *MemoryKeyStore {
	return &MemoryKeyStore{entries: make(map[string]*PublicKey)}
}

// Register stores a key for a domain/selector pair.
func (s *MemoryKeyStore) Register(domain, selector string, key crypto.PublicKey) {
	s.entries[keyStoreID(domain, selector)] = &PublicKey{Key: key}
}

// LookupKey implements KeyResolver.
func (s *MemoryKeyStore) LookupKey(domain, selector string) (*PublicKey, bool) {
	k, ok := s.entries[keyStoreID(domain, selector)]
	return k, ok
}

func keyStoreID(domain, selector string) string {
	return strings.ToLower(domain) + "/" + strings.ToLower(selector)
}

// DKIMResult captures the verification of one DKIM-Signature field.
type DKIMResult struct {
	// Index is the header block position of the DKIM-Signature field.
	Index     int
	Domain    string
	Selector  string
	Algorithm string
	// Valid reports full structural, temporal and cryptographic validity.
	Valid bool
	// BodyLengthLimit is the signed l= value, or -1 when absent.
	BodyLengthLimit int64
	// CanonicalBodyLength is the canonicalized body size in bytes.
	CanonicalBodyLength int64
	// Truncated is true when the canonical body extends past BodyLengthLimit.
	Truncated bool
	// FromEntries is how many times From appears in h=.
	FromEntries int
	// FromBound is how many concrete From fields were consumed.
	FromBound int
	Findings  []Finding
}

// ARCHopResult captures validation of one ARC instance (one AAR/AMS/AS set).
type ARCHopResult struct {
	Instance   int
	CV         string
	AARPresent bool
	AMSValid   bool
	SealValid  bool
	Findings   []Finding
}

// ARCResult captures full RFC 8617 chain validation.
type ARCResult struct {
	Present bool
	// Instances are the instance numbers observed on the wire, sorted.
	Instances []int
	// Hops holds per-instance detail indexed by instance number.
	Hops     map[int]*ARCHopResult
	Valid    bool
	Findings []Finding
}

// MessageVerdict is the final anti-spoofing decision for one message.
type MessageVerdict struct {
	// Accepted is true only when no policy-enabled rejection fired.
	Accepted bool
	// Reason summarizes the primary rejection cause when Accepted is false.
	Reason   string
	Findings []Finding
	DKIM     []DKIMResult
	ARC      *ARCResult
}

// HasCode reports whether the verdict (including nested component findings)
// contains a finding with the given code.
func (v *MessageVerdict) HasCode(code string) bool {
	for _, f := range v.Findings {
		if string(f.Code) == code {
			return true
		}
	}
	for _, r := range v.DKIM {
		for _, f := range r.Findings {
			if string(f.Code) == code {
				return true
			}
		}
	}
	if v.ARC != nil {
		for _, f := range v.ARC.Findings {
			if string(f.Code) == code {
				return true
			}
		}
		for _, h := range v.ARC.Hops {
			for _, f := range h.Findings {
				if string(f.Code) == code {
					return true
				}
			}
		}
	}
	return false
}

// Verifier runs offline DKIM/ARC verification under a fixed Policy.
type Verifier struct {
	keys   KeyResolver
	policy *Policy
}

// NewVerifier builds a Verifier. A nil policy selects DefaultPolicy.
func NewVerifier(keys KeyResolver, policy *Policy) *Verifier {
	if policy == nil {
		policy = DefaultPolicy()
	}
	if policy.Now == nil {
		policy.Now = time.Now
	}
	if policy.MaxARCHops == 0 {
		policy.MaxARCHops = 50
	}
	return &Verifier{keys: keys, policy: policy}
}
