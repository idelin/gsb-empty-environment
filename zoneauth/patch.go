package zoneauth

import (
	"bytes"
	"crypto/ed25519"
	"errors"
)

// OpKind enumerates the finite, manager-signed zone updates.
type OpKind string

const (
	// OpAddDelegation introduces a delegation cut at Target.
	OpAddDelegation OpKind = "add_delegation"
	// OpRemoveDelegation lifts the delegation cut at Target.
	OpRemoveDelegation OpKind = "remove_delegation"
	// OpSetWildcard installs/replaces/removes the "*" template under Target.
	OpSetWildcard OpKind = "set_wildcard"
)

// Operation is one finite update together with the evidence required to
// authenticate it against both the old and the new root. Each proof is a
// structural chain ending at the operation's parent or target; a missing
// node is represented by the parent's committed children map, never by a
// fabricated empty subtree.
type Operation struct {
	Kind   OpKind `json:"kind"`
	Target Name   `json:"target"`
	// OldProof covers Target (or its parent wildcard anchor) under the old
	// root. For removals it must prove the deleted node existed.
	OldProof *Proof `json:"old_proof"`
	// NewProof covers the same path under the new root. For additions it
	// must prove the new node becomes effective at the identical path.
	NewProof *Proof `json:"new_proof"`
}

// Patch is a signed, finite set of operations between two adjacent roots.
// Handoff is non-nil exactly when the zone key rotates in this revision.
type Patch struct {
	Zone    string        `json:"zone"`
	OldRoot *RootEnvelope `json:"old_root"`
	NewRoot *RootEnvelope `json:"new_root"`
	// NewApex is the committed apex node under the new root. The client
	// needs it to begin every new-root walk; it also authenticates unchanged
	// side branches whose child hashes are adopted unchanged.
	NewApex *TrieNode        `json:"new_apex"`
	Ops     []Operation      `json:"ops"`
	Handoff *HandoffEnvelope `json:"handoff,omitempty"`
	// ClientProofs are extra new-root partial proofs signed out for cached
	// queries whose prior evidence does not survive the update. They end at
	// the closest authenticated encloser and never fabricate a missing edge.
	ClientProofs []*Proof `json:"client_proofs,omitempty"`
}

// VerifiedRoot is the trust state produced by VerifyPatch.
type VerifiedRoot struct {
	Envelope *RootEnvelope
	Key      ed25519.PublicKey
	// TrustedKeys carries both the old key (for cached old-root proofs) and
	// the new key (for the new root).
	TrustedKeys map[string]ed25519.PublicKey
}

// Sentinel errors surfaced by the verifier. Callers use errors.Is.
var (
	// ErrPatchRootMismatch means the patch roots do not bind the cached root.
	ErrPatchRootMismatch = errors.New("zoneauth: patch root does not match cached root")
	// ErrPatchOrder means sequence numbers do not advance by exactly one.
	ErrPatchOrder = errors.New("zoneauth: patch sequence order invalid")
	// ErrPatchEvidence means an operation lacks valid old/new-root evidence.
	ErrPatchEvidence = errors.New("zoneauth: operation evidence missing or unauthenticated")
	// ErrPatchHandoff means the key handoff fails to chain the keys/roots.
	ErrPatchHandoff = errors.New("zoneauth: key handoff invalid")
	// ErrPatchEmpty means the patch carries no operations and no handoff.
	ErrPatchEmpty = errors.New("zoneauth: empty patch")
)

// VerifyPatch authenticates a patch against the client's cached root and key.
// It checks real Ed25519 signatures, root-hash bindings, the handoff chain,
// sequence ordering and every operation's old/new evidence. On success it
// returns the new trust state; nothing is accepted on partial evidence.
func VerifyPatch(p *Patch, cached *RootEnvelope, trusted map[string]ed25519.PublicKey) (*VerifiedRoot, error) {
	if len(p.Ops) == 0 && p.Handoff == nil {
		return nil, ErrPatchEmpty
	}
	if p.OldRoot == nil || p.NewRoot == nil {
		return nil, ErrPatchRootMismatch
	}
	if cached != nil {
		if p.OldRoot.Sequence != cached.Sequence || !bytes.Equal(p.OldRoot.RootHash, cached.RootHash) {
			return nil, ErrPatchRootMismatch
		}
	}
	if err := VerifyEnvelope(p.OldRoot, trusted); err != nil {
		return nil, err
	}
	if p.NewRoot.Sequence != p.OldRoot.Sequence+1 {
		return nil, ErrPatchOrder
	}
	if p.NewApex == nil || !bytes.Equal(HashNode(p.NewApex), p.NewRoot.RootHash) {
		return nil, errors.Join(ErrPatchEvidence, errors.New("new-root apex node missing or mismatched"))
	}

	newTrusted := make(map[string]ed25519.PublicKey, len(trusted)+1)
	for id, key := range trusted {
		newTrusted[id] = key
	}

	if p.Handoff != nil {
		oldPub, ok := trusted[p.OldRoot.KeyID]
		if !ok {
			return nil, ErrUntrustedKey
		}
		if err := VerifyHandoff(p.Handoff, oldPub); err != nil {
			return nil, errors.Join(ErrPatchHandoff, err)
		}
		if p.Handoff.Sequence != p.NewRoot.Sequence ||
			!bytes.Equal(p.Handoff.OldRootHash, p.OldRoot.RootHash) ||
			!bytes.Equal(p.Handoff.NewRootHash, p.NewRoot.RootHash) ||
			p.NewRoot.KeyID != p.Handoff.NewKeyID ||
			p.NewRoot.PreviousKeyID != p.OldRoot.KeyID {
			return nil, ErrPatchHandoff
		}
		newTrusted[p.Handoff.NewKeyID] = ed25519.PublicKey(p.Handoff.NewKey)
	} else {
		if p.NewRoot.KeyID != p.OldRoot.KeyID {
			return nil, ErrPatchHandoff
		}
	}

	if err := VerifyEnvelope(p.NewRoot, newTrusted); err != nil {
		return nil, err
	}

	for i := range p.Ops {
		if err := verifyOperation(&p.Ops[i], p); err != nil {
			return nil, errors.Join(ErrPatchEvidence, err)
		}
	}
	for _, proof := range p.ClientProofs {
		if proof == nil {
			return nil, errors.Join(ErrPatchEvidence, errors.New("nil client proof"))
		}
		if err := proof.VerifyStructural(p.NewRoot.RootHash); err != nil {
			return nil, errors.Join(ErrPatchEvidence, err)
		}
		if _, err := proof.DeriveAnswer(); err != nil {
			return nil, errors.Join(ErrPatchEvidence, err)
		}
	}

	return &VerifiedRoot{
		Envelope:    p.NewRoot,
		Key:         newTrusted[p.NewRoot.KeyID],
		TrustedKeys: newTrusted,
	}, nil
}

func verifyOperation(op *Operation, p *Patch) error {
	if len(op.Target) == 0 {
		return errors.New("operation may not target the apex")
	}
	switch op.Kind {
	case OpAddDelegation:
		return verifyAddDelegation(op, p)
	case OpRemoveDelegation:
		return verifyRemoveDelegation(op, p)
	case OpSetWildcard:
		return verifySetWildcard(op, p)
	default:
		return errors.New("unknown operation kind")
	}
}

// proveTarget verifies a partial proof against a root and requires its chain
// to reach targetDepth.
func proveTarget(proof *Proof, root *RootEnvelope, query Name, targetDepth int) error {
	if proof == nil {
		return errors.New("proof is absent")
	}
	if !proof.Query.Equal(query) {
		return errors.New("proof query mismatch")
	}
	if err := proof.VerifyStructural(root.RootHash); err != nil {
		return err
	}
	closest := proof.closestNode()
	if len(closest.Path) != targetDepth {
		return errors.New("proof does not reach the required depth")
	}
	return nil
}

// proveAbsence verifies a proof whose closest encloser is the parent of the
// target, with the target label authenticated as absent. This is the only
// legitimate "empty tree" claim: the parent node is explicitly provided.
func proveAbsence(proof *Proof, root *RootEnvelope, target Name) error {
	if proof == nil {
		return errors.New("absence proof is absent")
	}
	if err := proof.VerifyStructural(root.RootHash); err != nil {
		return err
	}
	parent := target.Parent()
	closest := proof.closestNode()
	if !closest.Path.Equal(parent) {
		return errors.New("absence proof must expose the target parent")
	}
	if _, exists := closest.Children[target[len(target)-1]]; exists {
		return errors.New("target is present where absence was claimed")
	}
	return nil
}

func verifyAddDelegation(op *Operation, p *Patch) error {
	if err := proveAbsence(op.OldProof, p.OldRoot, op.Target); err != nil {
		return errors.New("old-root " + err.Error())
	}
	if err := proveTarget(op.NewProof, p.NewRoot, op.Target, len(op.Target)); err != nil {
		return errors.New("new-root " + err.Error())
	}
	node := op.NewProof.closestNode()
	if !node.Delegate {
		return errors.New("new node is not a delegation")
	}
	if node.Records == nil || len(node.Records) == 0 {
		return errors.New("delegation carries no NS records")
	}
	return nil
}

func verifyRemoveDelegation(op *Operation, p *Patch) error {
	// The deleted cut must be proven to have existed under the old root.
	if err := proveTarget(op.OldProof, p.OldRoot, op.Target, len(op.Target)); err != nil {
		return errors.New("old-root " + err.Error())
	}
	if !op.OldProof.closestNode().Delegate {
		return errors.New("old node is not a delegation")
	}
	// Under the new root the same path is a regular node.
	if err := proveTarget(op.NewProof, p.NewRoot, op.Target, len(op.Target)); err != nil {
		return errors.New("new-root " + err.Error())
	}
	if op.NewProof.closestNode().Delegate {
		return errors.New("delegation still present under new root")
	}
	return nil
}

func verifySetWildcard(op *Operation, p *Patch) error {
	anchor := op.Target
	// Both proofs walk to the anchor. The anchor's committed children map is
	// the only thing that can authenticate either presence or absence of the
	// "*" edge; old-root proofs are allowed to stop at that anchor even when
	// the wildcard previously existed but was not queried.
	if err := op.OldProof.VerifyStructural(p.OldRoot.RootHash); err != nil {
		return errors.New("old-root " + err.Error())
	}
	if err := op.NewProof.VerifyStructural(p.NewRoot.RootHash); err != nil {
		return errors.New("new-root " + err.Error())
	}
	oldClosest := op.OldProof.closestNode()
	newClosest := op.NewProof.closestNode()
	// The old proof either stops at the anchor (wildcard was absent or not
	// traversed) or ends at the actual "*" node (wildcard existed and the
	// query walked its real edge).
	oldOK := oldClosest.Path.Equal(anchor) || oldClosest.Path.Equal(anchor.With("*"))
	newOK := newClosest.Path.Equal(anchor) || newClosest.Path.Equal(anchor.With("*"))
	if !oldOK || !newOK {
		return errors.New("wildcard anchor mismatch")
	}
	if !op.NewProof.Query.Equal(anchor.With("*")) {
		return errors.New("wildcard proof query mismatch")
	}
	newWild := op.NewProof.WildcardNode
	if newWild == nil {
		// Template removal: the anchor must authenticate the missing edge.
		if _, exists := newClosest.Children["*"]; exists {
			return errors.New("wildcard still present under new root")
		}
	} else {
		if !newWild.Path.Equal(anchor.With("*")) {
			return errors.New("new wildcard path mismatch")
		}
	}
	return nil
}
