package takeover

import (
	"encoding/json"
	"time"
)

// GrantID is the stable identity of one delegated capability.
type GrantID string

// Grant is one control capability. The same sequence set may not be delegated
// twice or reused after a return.
type Grant struct {
	ID             GrantID        `json:"id"`
	Parent         GrantID        `json:"parent,omitempty"`
	ProcessStartID ProcessStartID `json:"process_start_id"`
	SessionRoot    SessionRoot    `json:"session_root"`
	Signals        SignalSet      `json:"signals"`
	Dimensions     DimensionSet   `json:"dimensions"`
	Sequences      SeqSet         `json:"sequences"`
	Epoch          uint64         `json:"epoch"`
	IssuedAt       time.Time      `json:"issued_at,omitempty"`
}

// Control is an alias for Grant used by callers that model one control lease.
type Control = Grant

// Scope is a partial grant: a terminal-dimension set and a sequence set.
type Scope struct {
	Dimensions DimensionSet `json:"dimensions,omitempty"`
	Sequences  SeqSet       `json:"sequences"`
}

// IsEmpty reports whether the scope contains neither dimensions nor sequences.
func (s Scope) IsEmpty() bool {
	return s.Dimensions.IsEmpty() && s.Sequences.IsEmpty()
}

// Equal reports scope equality.
func (s Scope) Equal(other Scope) bool {
	return s.Dimensions.Equal(other.Dimensions) && s.Sequences.Equal(other.Sequences)
}

// Contains reports whether other is entirely inside s.
func (s Scope) Contains(other Scope) bool {
	return other.Dimensions.SubsetOf(s.Dimensions) && other.Sequences.SubsetOf(s.Sequences)
}

// Intersect returns the overlapping dimensions and sequences.
func (s Scope) Intersect(other Scope) Scope {
	return Scope{
		Dimensions: s.Dimensions.Intersect(other.Dimensions),
		Sequences:  s.Sequences.Intersect(other.Sequences),
	}
}

// Subtract removes other from s. Dimensions are compared as exact members.
func (s Scope) Subtract(other Scope) Scope {
	return Scope{
		Dimensions: s.Dimensions.Subtract(other.Dimensions),
		Sequences:  s.Sequences.Subtract(other.Sequences),
	}
}

// Union returns the union of both scopes.
func (s Scope) Union(other Scope) Scope {
	return Scope{
		Dimensions: s.Dimensions.Union(other.Dimensions),
		Sequences:  s.Sequences.Union(other.Sequences),
	}
}

// Validate checks the intrinsically invalid parts of a grant.
func (g Grant) Validate() error {
	if g.ID == "" {
		return &ValidationError{Field: "id", Err: ErrInvalidGrant}
	}
	if !g.ProcessStartID.Valid() {
		return &ValidationError{Field: "process_start_id", Err: ErrInvalidGrant}
	}
	if !g.SessionRoot.Valid() {
		return &ValidationError{Field: "session_root", Err: ErrInvalidGrant}
	}
	if g.Signals.IsEmpty() {
		return &ValidationError{Field: "signals", Err: ErrInvalidGrant}
	}
	for signal := range g.Signals {
		if signal == "" {
			return &ValidationError{Field: "signals", Err: ErrInvalidGrant}
		}
	}
	if g.Dimensions.IsEmpty() {
		return &ValidationError{Field: "dimensions", Err: ErrInvalidGrant}
	}
	if err := validateDimensionSet(g.Dimensions); err != nil {
		return err
	}
	if g.Sequences.IsEmpty() {
		return &ValidationError{Field: "sequences", Err: ErrInvalidGrant}
	}
	return nil
}

func validateDimensionSet(set DimensionSet) error {
	seen := make([]Dimension, 0, len(set))
	for dimension := range set {
		if !dimension.Valid() {
			return ErrInvalidDimension
		}
		for _, existing := range seen {
			if existing.Overlaps(dimension) {
				return &DimensionConflictError{A: existing, B: dimension}
			}
		}
		seen = append(seen, dimension)
	}
	return nil
}

// Scope returns the grant's current (initially full) scope.
func (g Grant) Scope() Scope {
	return Scope{Dimensions: g.Dimensions, Sequences: g.Sequences}
}

// DelegateRequest asks the holder of a parent grant to create a child grant.
// The requested scope must be a subset of the parent's remaining scope.
type DelegateRequest struct {
	GrantID        GrantID        `json:"grant_id"`
	ProcessStartID ProcessStartID `json:"process_start_id"`
	SessionRoot    SessionRoot    `json:"session_root"`
	Signals        SignalSet      `json:"signals"`
	Dimensions     DimensionSet   `json:"dimensions"`
	Sequences      SeqSet         `json:"sequences"`
}

// ReturnRequest returns all or part of a grant. Supplying dimensions or
// sequences implies a partial return even when Partial is left false. With no
// scope fields the whole residual scope is returned. Receipts and Seals settle
// active descendants whose scope overlaps the returned scope.
type ReturnRequest struct {
	GrantID    GrantID          `json:"grant_id"`
	Partial    bool             `json:"partial"`
	Dimensions DimensionSet     `json:"dimensions,omitempty"`
	Sequences  SeqSet           `json:"sequences,omitempty"`
	Receipts   []ReturnReceipt  `json:"receipts,omitempty"`
	Seals      []DescendantSeal `json:"seals,omitempty"`
	Reason     string           `json:"reason,omitempty"`
}

// ReturnResult describes the applied return.
type ReturnResult struct {
	GrantID  GrantID       `json:"grant_id"`
	Returned Scope         `json:"returned"`
	Retained Scope         `json:"retained"`
	Sealed   SeqSet        `json:"sealed"`
	Closed   bool          `json:"closed"`
	Receipt  ReturnReceipt `json:"receipt"`
	Settled  []GrantID     `json:"settled,omitempty"`
}

// DescendantSeal is an ancestor-issued fence for a descendant slice. The
// sequence field must cover every unconsumed sequence of that descendant in
// the affected slice. Dimensions names the revoked dimensional slice and is
// optional for settlement because the parent return already withdraws it.
type DescendantSeal struct {
	GrantID    GrantID      `json:"grant_id"`
	Dimensions DimensionSet `json:"dimensions,omitempty"`
	Sequences  SeqSet       `json:"sequences,omitempty"`
	Reason     string       `json:"reason,omitempty"`
}

// ReturnReceipt is a signed assertion by a descendant that it relinquished
// the named scope. A receipt for a parent does not cover its grandchildren;
// those descendants also require receipts or seals.
type ReturnReceipt struct {
	GrantID        GrantID        `json:"grant_id"`
	ProcessStartID ProcessStartID `json:"process_start_id"`
	SessionRoot    SessionRoot    `json:"session_root"`
	Epoch          uint64         `json:"epoch"`
	Returned       Scope          `json:"returned"`
	Retained       Scope          `json:"retained"`
	Final          bool           `json:"final"`
	Sealed         SeqSet         `json:"sealed"`
	IssuedAt       time.Time      `json:"issued_at"`
	Nonce          string         `json:"nonce"`
	KeyID          string         `json:"key_id"`
	Signature      []byte         `json:"signature,omitempty"`
}

type unsignedReturnReceipt struct {
	GrantID        GrantID        `json:"grant_id"`
	ProcessStartID ProcessStartID `json:"process_start_id"`
	SessionRoot    SessionRoot    `json:"session_root"`
	Epoch          uint64         `json:"epoch"`
	Returned       Scope          `json:"returned"`
	Retained       Scope          `json:"retained"`
	Final          bool           `json:"final"`
	Sealed         SeqSet         `json:"sealed"`
	IssuedAt       time.Time      `json:"issued_at"`
	Nonce          string         `json:"nonce"`
	KeyID          string         `json:"key_id"`
}

// CanonicalBytes returns the deterministic unsigned representation.
func (r ReturnReceipt) CanonicalBytes() ([]byte, error) {
	return canonicalJSON(unsignedReturnReceipt{
		GrantID:        r.GrantID,
		ProcessStartID: r.ProcessStartID,
		SessionRoot:    r.SessionRoot,
		Epoch:          r.Epoch,
		Returned:       r.Returned,
		Retained:       r.Retained,
		Final:          r.Final,
		Sealed:         r.Sealed,
		IssuedAt:       r.IssuedAt,
		Nonce:          r.Nonce,
		KeyID:          r.KeyID,
	})
}

// Sign fills KeyID and Signature.
func (r *ReturnReceipt) Sign(signer Signer) error {
	if signer == nil {
		return ErrReceiptSignature
	}
	r.KeyID = signer.KeyID()
	message, err := r.CanonicalBytes()
	if err != nil {
		return err
	}
	signature, err := signer.Sign(message)
	if err != nil {
		return err
	}
	r.Signature = signature
	return nil
}

// Verify checks the receipt signature and key identity.
func (r ReturnReceipt) Verify(verifier SignatureVerifier) error {
	if verifier == nil || r.KeyID == "" || len(r.Signature) == 0 {
		return ErrReceiptSignature
	}
	message, err := r.CanonicalBytes()
	if err != nil {
		return err
	}
	if !verifier.Verify(r.KeyID, message, r.Signature) {
		return ErrReceiptSignature
	}
	return nil
}

// MarshalJSON keeps Scope sets normalized.
func (s Scope) MarshalJSON() ([]byte, error) {
	type alias Scope
	return json.Marshal(alias(s))
}
