// Package attest composes already signature-verified build statements into
// admission proofs for a release service. It never trusts a signature alone:
// every requirement (source commit, pinned builder, complete material set)
// must be backed by an explicit proof path through the statement graph, and
// missing evidence is reported instead of guessed.
package attest

import (
	"errors"
	"fmt"
)

var (
	// ErrMissingProof is returned when no statement supplies evidence for a
	// requirement. The verifier fails closed and never infers a proof.
	ErrMissingProof = errors.New("attest: missing proof")
	// ErrCycle is returned when build statements reference each other's
	// outputs in a loop.
	ErrCycle = errors.New("attest: cycle in build statement graph")
	// ErrStaleContext is returned when a statement belongs to a different
	// release context than the policy expects (e.g. a replayed old release).
	ErrStaleContext = errors.New("attest: statement from unexpected release context")
	// ErrViolation is returned when a requirement is contradicted by the
	// statements (wrong commit, wrong builder).
	ErrViolation = errors.New("attest: policy requirement violated")
	// ErrInvalidInput is returned for malformed statements or policies.
	ErrInvalidInput = errors.New("attest: invalid input")
)

// Requirement identifiers used as keys in Result.Proofs.
const (
	ReqArtifactFromCommit = "artifact-from-commit"
	ReqPinnedBuilder      = "pinned-builder"
	ReqCompleteMaterials  = "complete-materials"
)

// Digest identifies content by algorithm and hex-encoded value.
type Digest struct {
	Algorithm string
	Value     string
}

func (d Digest) String() string { return d.Algorithm + ":" + d.Value }

// Resource is a named, content-addressed input or output of a build step.
type Resource struct {
	Name   string
	Digest Digest
}

// Statement is a single build attestation whose signature has already been
// verified by the caller. Multi-step builds chain statements together: the
// digest of one statement's output appears in a later statement's materials.
type Statement struct {
	ID        string
	Builder   string
	Commit    string
	Context   string // release context, e.g. "release/2026-11-11"
	Materials []Resource
	Outputs   []Resource
}

// Policy pins what the admission layer requires for a release.
type Policy struct {
	Artifact  Digest     // digest of the artifact being released
	Commit    string     // required source commit
	Builder   string     // pinned builder identity
	Context   string     // expected release context (replay guard)
	Materials []Resource // complete required material set, matched by name and digest
}

// ProofPath records the evidence chain backing one requirement.
type ProofPath struct {
	Requirement string
	Statements  []string // statement IDs, sources first, artifact last
	Detail      string
}

// Result maps each requirement identifier to its proof path.
type Result struct {
	Proofs map[string]ProofPath
}

// Verify composes statements into proofs for every policy requirement. It
// returns a non-nil error (wrapping one of the package sentinels) and no
// partial result whenever any requirement cannot be proven.
func Verify(statements []Statement, policy Policy) (*Result, error) {
	if policy.Artifact.Algorithm == "" || policy.Artifact.Value == "" {
		return nil, fmt.Errorf("%w: policy artifact digest is required", ErrInvalidInput)
	}
	if policy.Context == "" {
		return nil, fmt.Errorf("%w: policy release context is required", ErrInvalidInput)
	}

	byID := make(map[string]int, len(statements))
	byOutput := make(map[Digest]int)
	for i, s := range statements {
		if s.ID == "" {
			return nil, fmt.Errorf("%w: statement at index %d has empty ID", ErrInvalidInput, i)
		}
		if j, dup := byID[s.ID]; dup {
			return nil, fmt.Errorf("%w: statements %d and %d share ID %q", ErrInvalidInput, j, i, s.ID)
		}
		if s.Context != policy.Context {
			return nil, fmt.Errorf("%w: statement %q has context %q, want %q",
				ErrStaleContext, s.ID, s.Context, policy.Context)
		}
		byID[s.ID] = i
		for _, out := range s.Outputs {
			if j, dup := byOutput[out.Digest]; dup {
				return nil, fmt.Errorf("%w: output digest %s claimed by both %q and %q",
					ErrInvalidInput, out.Digest, statements[j].ID, s.ID)
			}
			byOutput[out.Digest] = i
		}
	}

	root, ok := byOutput[policy.Artifact]
	if !ok {
		return nil, fmt.Errorf("%w: no statement produces artifact %s", ErrMissingProof, policy.Artifact)
	}

	// Walk the chain from the released artifact back to its sources,
	// following material digests to the statements that produced them.
	var order []int
	visited := make(map[int]bool)
	inStack := make(map[int]bool)
	var visit func(idx int) error
	visit = func(idx int) error {
		if inStack[idx] {
			return fmt.Errorf("%w: statement %q references its own dependency chain",
				ErrCycle, statements[idx].ID)
		}
		if visited[idx] {
			return nil
		}
		inStack[idx] = true
		for _, m := range statements[idx].Materials {
			producer, found := byOutput[m.Digest]
			if !found {
				continue // external/source material: leaf of the chain
			}
			if err := visit(producer); err != nil {
				return err
			}
		}
		delete(inStack, idx)
		visited[idx] = true
		order = append(order, idx)
		return nil
	}
	if err := visit(root); err != nil {
		return nil, err
	}

	chainIDs := make([]string, len(order))
	for i, idx := range order {
		chainIDs[i] = statements[idx].ID
	}
	proofs := make(map[string]ProofPath, 3)

	for _, idx := range order {
		if statements[idx].Commit != policy.Commit {
			return nil, fmt.Errorf("%w: %s: statement %q built commit %q, want %q",
				ErrViolation, ReqArtifactFromCommit, statements[idx].ID,
				statements[idx].Commit, policy.Commit)
		}
	}
	proofs[ReqArtifactFromCommit] = ProofPath{
		Requirement: ReqArtifactFromCommit,
		Statements:  chainIDs,
		Detail:      fmt.Sprintf("artifact %s traced to source commit %s", policy.Artifact, policy.Commit),
	}

	for _, idx := range order {
		if statements[idx].Builder != policy.Builder {
			return nil, fmt.Errorf("%w: %s: statement %q used builder %q, want pinned %q",
				ErrViolation, ReqPinnedBuilder, statements[idx].ID,
				statements[idx].Builder, policy.Builder)
		}
	}
	proofs[ReqPinnedBuilder] = ProofPath{
		Requirement: ReqPinnedBuilder,
		Statements:  chainIDs,
		Detail:      fmt.Sprintf("all %d step(s) built by pinned builder %q", len(order), policy.Builder),
	}

	type materialKey struct {
		name   string
		digest Digest
	}
	supplied := make(map[materialKey]string)
	for _, idx := range order {
		for _, m := range statements[idx].Materials {
			supplied[materialKey{m.Name, m.Digest}] = statements[idx].ID
		}
	}
	materialStmts := make([]string, 0, len(policy.Materials))
	seen := make(map[string]bool)
	for _, req := range policy.Materials {
		src, found := supplied[materialKey{req.Name, req.Digest}]
		if !found {
			return nil, fmt.Errorf("%w: %s: material %q (%s) is not proven by any statement",
				ErrMissingProof, ReqCompleteMaterials, req.Name, req.Digest)
		}
		if !seen[src] {
			seen[src] = true
			materialStmts = append(materialStmts, src)
		}
	}
	proofs[ReqCompleteMaterials] = ProofPath{
		Requirement: ReqCompleteMaterials,
		Statements:  materialStmts,
		Detail:      fmt.Sprintf("all %d required material(s) covered by name and digest", len(policy.Materials)),
	}

	return &Result{Proofs: proofs}, nil
}
