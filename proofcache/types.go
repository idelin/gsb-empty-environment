package proofcache

import (
	"fmt"
	"sort"
)

// Label identifies an analysis target, typically a function in the host
// analyzer. Labels are scoped by the host; the cache never interprets them.
type Label string

// RuleVersion identifies the exact set of verification rules used to produce
// a proof. Any change to the rule set, including soundness-relevant
// configuration, must use a new version so old proofs stop validating.
type RuleVersion struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// String renders the rule version for diagnostics.
func (r RuleVersion) String() string {
	return r.Name + "@" + r.Version
}

// DepClaim is one edge in a proof's dependency closure. It binds the callee
// label to the exact contract digest the proof was checked against. A change
// to the callee's contract therefore changes the claim and invalidates every
// dependent proof even if the IR digest is unchanged.
type DepClaim struct {
	Label    Label
	Contract Digest
}

// Fingerprint is the full content identity a proof is bound to: the input IR
// digest, every dependency's contract digest and the verification rule
// version. Two proofs are interchangeable only when their fingerprints are
// byte-identical.
//
// The same logical fingerprint always canonicalizes to the same digest:
// dependency claims are sorted and duplicates are rejected so claim order
// cannot cause cache fragmentation or confusion.
type Fingerprint struct {
	IR   Digest
	Deps []DepClaim
	Rule RuleVersion

	sum Digest
}

// NewFingerprint builds a fingerprint and precomputes its canonical digest.
// Duplicate dependency labels with differing contracts are rejected.
func NewFingerprint(ir Digest, deps []DepClaim, rule RuleVersion) (Fingerprint, error) {
	if ir.IsZero() {
		return Fingerprint{}, fmt.Errorf("%w: zero IR digest", ErrInvalidArgument)
	}
	if rule.Name == "" || rule.Version == "" {
		return Fingerprint{}, fmt.Errorf("%w: rule version must be fully specified", ErrInvalidArgument)
	}
	canonical, err := canonicalDeps(deps)
	if err != nil {
		return Fingerprint{}, err
	}
	fp := Fingerprint{IR: ir, Deps: canonical, Rule: rule}
	fp.sum = fp.compute()
	return fp, nil
}

// Sum returns the canonical digest of the fingerprint.
func (f Fingerprint) Sum() Digest {
	return f.sum
}

func (f Fingerprint) compute() Digest {
	h := NewHasher()
	h.WriteDigest("ir", f.IR)
	for _, dep := range f.Deps {
		dh := NewHasher()
		dh.WriteString("dep-label", string(dep.Label))
		dh.WriteDigest("dep-contract", dep.Contract)
		h.WriteDigest("dep", dh.Sum())
	}
	_ = h.WriteJSON("rule", f.Rule)
	return h.Sum()
}

// Proof is the analyzer-produced proof payload. The cache treats it as opaque
// bytes but includes its digest in the stored binding so an entry cannot be
// re-pointed at a different proof after publication.
type Proof []byte

// Digest returns the domain-separated digest of the proof payload.
func (p Proof) Digest() Digest {
	h := NewHasher()
	h.WriteString("proof", string(p))
	return h.Sum()
}

// Entry is an immutable published cache entry.
type Entry struct {
	Label Label
	Fingerprint
	Proof   Proof
	BatchID string
	Epoch   uint64

	binding Digest
}

// Binding returns the integrity digest stored when the entry was published.
// Lookup recomputes this digest and rejects any entry whose recomputation
// differs, so tampered or partially overwritten storage cannot validate.
func (e *Entry) Binding() Digest {
	return e.binding
}

// cloneEntry returns a deep copy whose slices are not shared with the cache.
func cloneEntry(e *entry) *Entry {
	out := &Entry{
		Label:   e.label,
		Proof:   append(Proof(nil), e.proof...),
		BatchID: e.batchID,
		Epoch:   e.epoch,
		binding: e.binding,
	}
	out.IR = e.fp.IR
	out.Rule = e.fp.Rule
	out.sum = e.fp.sum
	out.Deps = append([]DepClaim(nil), e.fp.Deps...)
	return out
}

// entry is the internal mutable storage record.
type entry struct {
	label    Label
	fp       Fingerprint
	contract Digest
	proof    Proof
	batchID  string
	epoch    uint64
	binding  Digest
}

// bindingDigest folds every integrity-relevant field, including the proof
// bytes and the entry's own externally visible contract digest.
func bindingDigest(label Label, fp Fingerprint, contract Digest, proof Proof, batchID string, epoch uint64) Digest {
	h := NewHasher()
	h.WriteString("label", string(label))
	h.WriteDigest("fingerprint", fp.Sum())
	h.WriteDigest("contract", contract)
	h.WriteDigest("proof", proof.Digest())
	h.WriteString("batch", batchID)
	_ = h.WriteJSON("epoch", epoch)
	return h.Sum()
}

func canonicalDeps(deps []DepClaim) ([]DepClaim, error) {
	if len(deps) == 0 {
		return nil, nil
	}
	out := append([]DepClaim(nil), deps...)
	for _, dep := range out {
		if dep.Label == "" {
			return nil, fmt.Errorf("%w: dependency has empty label", ErrInvalidArgument)
		}
		if dep.Contract.IsZero() {
			return nil, fmt.Errorf("%w: dependency %q has zero contract", ErrInvalidArgument, dep.Label)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Label < out[j].Label })
	for i := 1; i < len(out); i++ {
		if out[i].Label == out[i-1].Label && out[i].Contract != out[i-1].Contract {
			return nil, fmt.Errorf("%w: conflicting contracts for dependency %q", ErrInvalidArgument, out[i].Label)
		}
	}
	deduped := out[:0]
	for _, dep := range out {
		if len(deduped) > 0 && deduped[len(deduped)-1].Label == dep.Label {
			continue
		}
		deduped = append(deduped, dep)
	}
	return deduped, nil
}

func sortedLabels(labels []Label) []Label {
	out := append([]Label(nil), labels...)
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}
