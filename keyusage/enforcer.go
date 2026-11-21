package keyusage

import (
	"fmt"
	"time"
)

// Target identifies what an operation acts on.
type Target int

const (
	// TargetBusinessData means ordinary application data: wrapping purposes
	// must never be applied to this target.
	TargetBusinessData Target = iota

	// TargetKeyMaterial means another key's key material: wrap/unwrap
	// purposes apply only to this target.
	TargetKeyMaterial
)

func (t Target) String() string {
	switch t {
	case TargetKeyMaterial:
		return "key_material"
	case TargetBusinessData:
		return "business_data"
	default:
		return "unknown"
	}
}

// Operation is a requested cryptographic call. Purpose must be exactly one of
// the single-attempt Purpose bits.
type Operation struct {
	Purpose   Purpose
	Target    Target
	Algorithm Algorithm // optional override; zero value falls back to the key's algorithm
}

// Policy enforces purpose constraints and algorithm restrictions.
type Policy struct {
	whitelist map[Algorithm]bool
	audit     AuditSink
}

// PolicyOption customizes a Policy at construction time.
type PolicyOption func(*Policy)

// WithWhitelist replaces the default strong-algorithm whitelist.
func WithWhitelist(algorithms ...Algorithm) PolicyOption {
	return func(p *Policy) {
		p.whitelist = make(map[Algorithm]bool, len(algorithms))
		for _, alg := range algorithms {
			p.whitelist[alg] = true
		}
	}
}

// WithAuditSink installs a custom rejection audit sink.
func WithAuditSink(sink AuditSink) PolicyOption {
	return func(p *Policy) {
		p.audit = sink
	}
}

// NewPolicy builds an enforcer using the default strong-algorithm whitelist
// and an in-memory audit sink unless overridden.
func NewPolicy(options ...PolicyOption) *Policy {
	policy := &Policy{
		whitelist: DefaultWhitelist(),
		audit:     &MemoryAuditSink{},
	}
	for _, option := range options {
		option(policy)
	}
	return policy
}

// AuditSink exposes the configured rejection sink.
func (p *Policy) AuditSink() AuditSink {
	return p.audit
}

// Whitelisted reports whether the algorithm is permitted by the whitelist.
// Weak algorithms are still rejected separately by Check/Derive regardless of
// whitelist membership.
func (p *Policy) Whitelisted(alg Algorithm) bool {
	return p.whitelist[alg]
}

// Check validates a call against the key's declared purposes, the target type
// and the algorithm policy. A nil return means the call is allowed; otherwise
// the rejection is written to the audit sink and the wrapped sentinel error is
// returned.
func (p *Policy) Check(key *Key, op Operation) error {
	if key == nil {
		return fmt.Errorf("%w: key is nil", ErrInvalidKey)
	}
	if op.Purpose == PurposeNone || op.Purpose&(op.Purpose-1) != 0 {
		return fmt.Errorf("%w: operation must request exactly one purpose, got %s", ErrInvalidOperation, op.Purpose)
	}

	alg := op.Algorithm
	if alg == "" {
		alg = key.Algorithm
	}

	if reason, weak := weakReason(alg); weak {
		return p.deny(key, alg, op, ErrWeakAlgorithm, reason)
	}
	if !p.Whitelisted(alg) {
		return p.deny(key, alg, op, ErrAlgorithmNotAllowed,
			fmt.Sprintf("algorithm %q is not in the policy whitelist", alg))
	}

	// Wrapping purposes are valid only against key material, never business
	// data, and wrapping-only keys must not decrypt business data.
	if (op.Purpose == PurposeWrapKey || op.Purpose == PurposeUnwrapKey) &&
		op.Target != TargetKeyMaterial {
		return p.deny(key, alg, op, ErrWrappingKeyMisuse,
			"wrap/unwrap operations may only target key material")
	}
	if op.Target == TargetBusinessData &&
		(op.Purpose == PurposeEncrypt || op.Purpose == PurposeDecrypt) &&
		!key.Purposes.contains(op.Purpose) &&
		key.Purposes.contains(PurposeWrapKey|PurposeUnwrapKey) {
		return p.deny(key, alg, op, ErrWrappingKeyMisuse,
			"key wrapping key must not encrypt or decrypt business data")
	}

	// Defense in depth: derived keys can never exceed their parent's purposes,
	// even if the key object was constructed outside Derive.
	if key.Parent != nil && !key.Parent.Purposes.contains(key.Purposes) {
		return p.deny(key, alg, op, ErrDerivedPurposeExceedsParent,
			fmt.Sprintf("derived key purposes %s exceed parent purposes %s", key.Purposes, key.Parent.Purposes))
	}

	if !key.Purposes.contains(op.Purpose) {
		return p.deny(key, alg, op, ErrPurposeNotAllowed,
			fmt.Sprintf("key %q declares purposes %s, which do not include %s on %s",
				key.ID, key.Purposes, op.Purpose, op.Target))
	}

	return nil
}

// Derive validates a derivation request and returns the derived key. The
// parent must hold PurposeDeriveKey, and the child's purposes must be a subset
// of the parent's purposes. Rejections are audited and no key is returned.
func (p *Policy) Derive(parent *Key, childID string, alg Algorithm, childPurposes Purpose) (*Key, error) {
	if parent == nil {
		return nil, fmt.Errorf("%w: parent key is nil", ErrInvalidKey)
	}
	if childID == "" {
		return nil, fmt.Errorf("%w: derived key id is empty", ErrInvalidKey)
	}
	if childPurposes == PurposeNone {
		return nil, fmt.Errorf("%w: derived key must declare at least one purpose", ErrInvalidOperation)
	}

	op := Operation{Purpose: PurposeDeriveKey, Target: TargetKeyMaterial, Algorithm: alg}

	if reason, weak := weakReason(alg); weak {
		return nil, p.deny(parent, alg, op, ErrWeakAlgorithm, reason)
	}
	if !p.Whitelisted(alg) {
		return nil, p.deny(parent, alg, op, ErrAlgorithmNotAllowed,
			fmt.Sprintf("algorithm %q is not in the policy whitelist", alg))
	}

	if !parent.Purposes.contains(PurposeDeriveKey) {
		return nil, p.deny(parent, alg, op, ErrDerivationNotAllowed,
			fmt.Sprintf("parent key %q purposes %s do not include derive_key", parent.ID, parent.Purposes))
	}
	if !parent.Purposes.contains(childPurposes) {
		return nil, p.deny(parent, alg, op, ErrDerivedPurposeExceedsParent,
			fmt.Sprintf("requested child purposes %s are not a subset of parent purposes %s",
				childPurposes, parent.Purposes))
	}

	child := &Key{
		ID:        childID,
		Algorithm: alg,
		Purposes:  childPurposes,
		Parent:    parent,
	}
	return child, nil
}

// deny records the rejection in the audit sink and returns the wrapped,
// context-rich error for the caller.
func (p *Policy) deny(key *Key, alg Algorithm, op Operation, cause error, reason string) error {
	event := AuditEvent{
		Time:      time.Now().UTC(),
		KeyID:     key.ID,
		Algorithm: alg,
		Purpose:   op.Purpose,
		Target:    op.Target,
		Reason:    reason,
		Err:       cause,
	}
	p.audit.Record(event)
	return fmt.Errorf("%w: key=%s alg=%s purpose=%s target=%s: %s",
		cause, key.ID, alg, op.Purpose, op.Target, reason)
}
