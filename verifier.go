package netsec

import (
	"context"
	"fmt"
	"time"
)

// Bundle is the set of step proofs a host presents to prove one completed
// authorization. The proofs need not be pre-sorted: the validator rebuilds
// the chain from the parent digests.
type Bundle struct {
	Proofs []Proof
}

// Verifier checks a proof's signature over its canonical content. Hosts plug
// in their own public-key implementation; the package ships a deterministic
// HMAC-based pseudo verifier for tests and local integration.
type Verifier interface {
	VerifyProof(ctx context.Context, p Proof) (bool, error)
}

// ReplayCache remembers proof digests that have already been consumed by a
// successful completion. Implementations must be safe for concurrent use.
type ReplayCache interface {
	Seen(ctx context.Context, digest Digest) (bool, error)
	Mark(ctx context.Context, digest Digest) error
}

// Validator combines a signature verifier with an optional replay cache.
type Validator struct {
	verifier Verifier
	replay   ReplayCache
}

// NewValidator builds a Validator. A nil replay cache disables cross-bundle
// replay protection; duplicate digests within one bundle are always rejected.
func NewValidator(verifier Verifier, replay ReplayCache) *Validator {
	return &Validator{verifier: verifier, replay: replay}
}

// Completion is the result of a successfully validated bundle.
type Completion struct {
	// OpDigest is the single operation every proof is bound to.
	OpDigest Digest
	// SessionID is the single session every proof belongs to.
	SessionID string
	// CoveredSteps are the graph step ids in chain order.
	CoveredSteps []string
	// ChainOrder is the proof digests from root to the latest step.
	ChainOrder []Digest
	// EffectiveFrom and EffectiveUntil bound the intersection of all windows.
	EffectiveFrom  time.Time
	EffectiveUntil time.Time
}

type indexedProof struct {
	proof  Proof
	digest Digest
}

// Complete verifies that bundle completes graph. A non-zero at additionally
// requires the common validity window to contain that instant; a zero at
// skips the instant check while still computing the intersection.
//
// On success, when a replay cache is configured, every proof digest is marked
// consumed. No digest is marked when any check fails.
func (v *Validator) Complete(ctx context.Context, graph StepGraph, bundle Bundle, at time.Time) (*Completion, error) {
	if err := graph.Validate(); err != nil {
		return nil, err
	}
	if v == nil || v.verifier == nil {
		return nil, ErrNilVerifier
	}
	if len(bundle.Proofs) == 0 {
		return nil, ErrEmptyBundle
	}

	indexed, err := v.indexProofs(ctx, bundle)
	if err != nil {
		return nil, err
	}
	if err := v.checkBindingsAndSignatures(ctx, indexed); err != nil {
		return nil, err
	}

	chain, err := rebuildChain(indexed)
	if err != nil {
		return nil, err
	}
	from, until, err := intersectWindows(indexed)
	if err != nil {
		return nil, err
	}
	if !at.IsZero() && (at.Before(from) || at.After(until)) {
		return nil, ErrNotEffective
	}
	if err := checkGraphCoverage(graph, chain, indexed); err != nil {
		return nil, err
	}
	if err := v.checkReplay(ctx, chain); err != nil {
		return nil, err
	}

	if v.replay != nil {
		for _, digest := range chain {
			if err := v.replay.Mark(ctx, digest); err != nil {
				return nil, fmt.Errorf("witness: mark replay digest: %w", err)
			}
		}
	}

	steps := make([]string, 0, len(chain))
	for _, digest := range chain {
		steps = append(steps, indexed[digest].proof.StepID)
	}
	return &Completion{
		OpDigest:       indexed[chain[0]].proof.OpDigest,
		SessionID:      indexed[chain[0]].proof.SessionID,
		CoveredSteps:   steps,
		ChainOrder:     append([]Digest(nil), chain...),
		EffectiveFrom:  from,
		EffectiveUntil: until,
	}, nil
}

func (v *Validator) indexProofs(ctx context.Context, bundle Bundle) (map[Digest]indexedProof, error) {
	indexed := make(map[Digest]indexedProof, len(bundle.Proofs))
	for _, proof := range bundle.Proofs {
		if proof.StepID == "" || proof.SessionID == "" {
			return nil, fmt.Errorf("%w: step or session is empty", ErrInvalidProof)
		}
		if proof.NotBefore.IsZero() || proof.NotAfter.IsZero() || !proof.NotBefore.Before(proof.NotAfter) {
			return nil, digestOrInvalid(proof, ErrInvalidWindow)
		}
		digest, err := proof.Digest()
		if err != nil {
			return nil, err
		}
		if _, dup := indexed[digest]; dup {
			return nil, digestContextError(ErrReplayedProof, digest)
		}
		indexed[digest] = indexedProof{proof: proof, digest: digest}
	}
	return indexed, nil
}

func digestOrInvalid(proof Proof, cause error) error {
	digest, err := proof.Digest()
	if err != nil {
		return err
	}
	return digestContextError(cause, digest)
}

func (v *Validator) checkBindingsAndSignatures(ctx context.Context, indexed map[Digest]indexedProof) error {
	var opDigest Digest
	var session string
	first := true
	for _, item := range indexed {
		proof := item.proof
		if first {
			opDigest = proof.OpDigest
			session = proof.SessionID
			first = false
		} else {
			if proof.OpDigest != opDigest {
				return digestContextError(ErrOpMismatch, item.digest)
			}
			if proof.SessionID != session {
				return digestContextError(ErrSessionMismatch, item.digest)
			}
		}
		ok, err := v.verifier.VerifyProof(ctx, proof)
		if err != nil {
			return fmt.Errorf("%w: %w", ErrBadSignature, err)
		}
		if !ok {
			return digestContextError(ErrBadSignature, item.digest)
		}
	}
	return nil
}

// rebuildChain enforces that the bundle is exactly one linear chain rooted at
// a zero-parent proof. Multiple roots, dangling parents, two proofs sharing a
// parent (a fork/merge attempt) or unreachable proofs are all rejected.
func rebuildChain(indexed map[Digest]indexedProof) ([]Digest, error) {
	successor := make(map[Digest]Digest, len(indexed))
	var root Digest
	rootCount := 0
	for digest, item := range indexed {
		parent := item.proof.ParentDigest
		if parent.IsZero() {
			root = digest
			rootCount++
			continue
		}
		if _, ok := indexed[parent]; !ok {
			return nil, digestContextError(ErrBrokenChain, digest)
		}
		if existing, ok := successor[parent]; ok && existing != digest {
			return nil, digestContextError(ErrForkedChain, parent)
		}
		successor[parent] = digest
	}
	if rootCount == 0 {
		return nil, ErrMissingRoot
	}
	if rootCount > 1 {
		return nil, ErrMultipleRoots
	}

	order := make([]Digest, 0, len(indexed))
	visited := make(map[Digest]struct{}, len(indexed))
	current := root
	for {
		if _, seen := visited[current]; seen {
			return nil, digestContextError(ErrOrphanProof, current)
		}
		visited[current] = struct{}{}
		order = append(order, current)
		next, ok := successor[current]
		if !ok {
			break
		}
		current = next
	}
	if len(visited) != len(indexed) {
		for digest := range indexed {
			if _, seen := visited[digest]; !seen {
				return nil, digestContextError(ErrOrphanProof, digest)
			}
		}
	}
	return order, nil
}

// intersectWindows returns the common time window: the latest NotBefore and
// the earliest NotAfter. An empty intersection means the proofs cannot
// jointly witness one moment.
func intersectWindows(indexed map[Digest]indexedProof) (time.Time, time.Time, error) {
	var from time.Time
	var until time.Time
	first := true
	for _, item := range indexed {
		start := item.proof.NotBefore.UTC()
		end := item.proof.NotAfter.UTC()
		if first || start.After(from) {
			from = start
		}
		if first || end.Before(until) {
			until = end
		}
		first = false
	}
	if from.After(until) {
		return time.Time{}, time.Time{}, ErrNoWindowOverlap
	}
	return from, until, nil
}

func checkGraphCoverage(graph StepGraph, chain []Digest, indexed map[Digest]indexedProof) error {
	// Map step -> chain position. Earlier helpers iterate maps, so all
	// ordering decisions use the deterministic chain slice.
	position := make(map[string]int, len(chain))
	for idx, digest := range chain {
		step := indexed[digest].proof.StepID
		if !graph.HasStep(step) {
			return unknownStepError(step)
		}
		if _, dup := position[step]; dup {
			return duplicateStepError(step)
		}
		position[step] = idx
	}

	for _, step := range graph.Steps {
		if _, ok := position[step]; !ok {
			return missingStepError(step)
		}
	}
	for step, prereqs := range graph.Edges {
		successorPos := position[step]
		for _, prereq := range prereqs {
			if position[prereq] >= successorPos {
				return edgeOrderError(step, prereq)
			}
		}
	}
	return nil
}

func (v *Validator) checkReplay(ctx context.Context, chain []Digest) error {
	if v.replay == nil {
		return nil
	}
	for _, digest := range chain {
		seen, err := v.replay.Seen(ctx, digest)
		if err != nil {
			return fmt.Errorf("witness: replay lookup: %w", err)
		}
		if seen {
			return digestContextError(ErrReplayedProof, digest)
		}
	}
	return nil
}
