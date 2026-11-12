package netsec

import (
	"context"
	"errors"
	"fmt"
)

// Policy decides whether a pre-receive push is admissible.
//
// A push is admitted only when every commit reachable from any updated ref's
// new value but unreachable from its old value has a valid signature produced
// by an allowed signer, and the aggregate object size of those commits fits
// the shared quota. The zero value is not usable; construct one with
// [NewPolicy].
type Policy struct {
	repo   Repository
	quota  *Quota
	verify VerifySignature

	// AllowedSigners maps a signer identity, as returned by Verify, to true.
	// It must not be mutated concurrently with calls to Admit. A valid
	// signature from any identity present in the map is accepted.
	AllowedSigners map[string]bool
}

// NewPolicy builds a Policy over repo using the shared quota and signature
// verifier. allowed lists the signer identities permitted to author commits.
func NewPolicy(repo Repository, quota *Quota, verify VerifySignature, allowed ...string) *Policy {
	signers := make(map[string]bool, len(allowed))
	for _, identity := range allowed {
		signers[identity] = true
	}
	return &Policy{
		repo:           repo,
		quota:          quota,
		verify:         verify,
		AllowedSigners: signers,
	}
}

// Violation describes one reason a push could not be admitted.
type Violation struct {
	// Ref is the update whose introduced set contains Commit, or "" when the
	// commit was reached through graph traversal from another ref.
	Ref string
	// Commit is the offending commit.
	Commit OID
	// Err is one of ErrUnsignedCommit, ErrSignerNotAllowed, or the verifier's
	// own error; it also wraps ErrObjectNotFound for missing objects.
	Err error
}

func (v Violation) Error() string {
	return fmt.Sprintf("netsec: ref %q commit %s rejected: %v", v.Ref, v.Commit, v.Err)
}

func (v Violation) Unwrap() error { return v.Err }

// introducedEntry records a commit newly reachable from a push and the first
// ref update that introduced it.
type introducedEntry struct {
	commit Commit
	ref    string
}

// AdmissionError is returned by Admit when a push is rejected. Use errors.Is
// against the package sentinels to distinguish the cause.
type AdmissionError struct {
	// Update is the offending ref update for graph/signature failures.
	Update RefUpdate
	// Violations holds each offending commit; empty for quota failures.
	Violations []Violation
	// Needed and Available describe the failed quota reservation.
	Needed    int64
	Available int64
	// Err is the underlying sentinel (ErrQuotaExhausted or ErrObjectNotFound).
	Err error
}

func (e *AdmissionError) Error() string {
	switch {
	case errors.Is(e.Err, ErrQuotaExhausted):
		return fmt.Sprintf("netsec: push rejected, needs %d bytes but only %d available: %v",
			e.Needed, e.Available, e.Err)
	case len(e.Violations) > 0:
		return fmt.Sprintf("netsec: push rejected with %d signature violation(s): %v",
			len(e.Violations), e.Violations[0].Err)
	default:
		return fmt.Sprintf("netsec: push rejected: %v", e.Err)
	}
}

func (e *AdmissionError) Unwrap() error {
	if e.Err != nil {
		return e.Err
	}
	if len(e.Violations) > 0 {
		return e.Violations[0].Err
	}
	return nil
}

// Admit evaluates one atomic push, which may contain several ref updates, and
// enforces the signature and quota rules.
//
// Semantics:
//
//   - For each non-deletion update it walks the full commit graph (all
//     parents) starting at New and stops at commits already reachable from Old
//     (the update's cut), so the exact new-reachable/old-unreachable set is
//     examined rather than the first-parent line.
//   - Deletion updates introduce no objects and are ignored.
//   - A commit introduced by several refs is verified and counted once.
//   - The total object byte size is reserved from the shared quota in one
//     atomic step; if the quota cannot cover the push, nothing is reserved.
//   - Any rejection releases the reservation before returning, leaving the
//     quota untouched. On success the returned Grant owns the reserved bytes
//     until the host calls Commit (objects stored) or Release (push aborted).
func (p *Policy) Admit(ctx context.Context, updates []RefUpdate) (*Grant, error) {
	if p == nil || p.repo == nil {
		return nil, errors.New("netsec: policy requires a repository")
	}
	if p.quota == nil {
		return nil, errors.New("netsec: policy requires a quota")
	}
	if p.verify == nil {
		return nil, errors.New("netsec: policy requires a signature verifier")
	}

	// Phase 1: graph traversal. Collect every newly introduced commit across
	// all updates, remembering the first ref that introduced each one.
	introduced := make(map[OID]introducedEntry)
	cuts := make(map[OID]struct{})

	for _, update := range updates {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if update.IsDeletion() {
			continue
		}

		// Everything reachable from Old is the already-accepted cut for this
		// update, including the zero OID for a freshly created ref.
		if !update.Old.IsZero() {
			oldCommit, err := p.repo.LookupCommit(ctx, update.Old)
			if err != nil {
				return nil, p.objectError(update, update.Old, err)
			}
			if err := p.markReachable(ctx, oldCommit, cuts); err != nil {
				return nil, err
			}
		}

		newCommit, err := p.repo.LookupCommit(ctx, update.New)
		if err != nil {
			return nil, p.objectError(update, update.New, err)
		}
		if err := p.collectIntroduced(ctx, newCommit, update.Ref, cuts, introduced); err != nil {
			return nil, err
		}
	}

	// Phase 2: atomically reserve the aggregate object size of the push. The
	// reservation is made before signature checks so a failed admission
	// demonstrates the reserve-then-release guarantee; reserve itself never
	// partially mutates state.
	var total int64
	for _, entry := range introduced {
		total += entry.commit.Size()
	}
	grant, err := p.quota.reserve(total)
	if err != nil {
		return nil, &AdmissionError{
			Needed:    total,
			Available: p.quota.Available(),
			Err:       ErrQuotaExhausted,
		}
	}

	// Phase 3: every introduced commit must be validly signed by an allowed
	// signer. On any failure release the reservation so the rejected push
	// returns all of its bytes.
	var violations []Violation
	for id, entry := range introduced {
		if err := ctx.Err(); err != nil {
			grant.Release()
			return nil, err
		}

		signer, verifyErr := p.verify(ctx, entry.commit)
		if verifyErr != nil {
			violations = append(violations, Violation{
				Ref: entry.ref, Commit: id,
				Err: fmt.Errorf("%w: %v", ErrUnsignedCommit, verifyErr),
			})
			continue
		}
		if !p.AllowedSigners[signer] {
			violations = append(violations, Violation{
				Ref: entry.ref, Commit: id,
				Err: fmt.Errorf("%w: signer %q", ErrSignerNotAllowed, signer),
			})
		}
	}

	if len(violations) > 0 {
		grant.Release()
		return nil, &AdmissionError{Violations: violations}
	}
	return grant, nil
}

// markReachable marks id and all of its ancestors as part of the already
// accepted cut. The cut is shared across updates so a commit reachable from
// any old value is never re-examined.
func (p *Policy) markReachable(ctx context.Context, start Commit, cut map[OID]struct{}) error {
	stack := []Commit{start}
	for len(stack) > 0 {
		cur := stack[len(stack)-1]
		stack = stack[:len(stack)-1]

		if _, ok := cut[cur.ID()]; ok {
			continue
		}
		cut[cur.ID()] = struct{}{}

		for _, parent := range cur.Parents() {
			if parent.IsZero() {
				continue
			}
			if _, ok := cut[parent]; ok {
				continue
			}
			next, err := p.repo.LookupCommit(ctx, parent)
			if err != nil {
				return fmt.Errorf("netsec: traverse cut: %w", err)
			}
			stack = append(stack, next)
		}
	}
	return nil
}

// collectIntroduced performs a full-graph DFS from start, stopping at the cut,
// and records each new commit exactly once. Merge parents are traversed, so an
// unsigned commit on a side branch cannot slip past a first-parent check.
func (p *Policy) collectIntroduced(ctx context.Context, start Commit, ref string,
	cut map[OID]struct{}, introduced map[OID]introducedEntry,
) error {
	stack := []Commit{start}
	for len(stack) > 0 {
		cur := stack[len(stack)-1]
		stack = stack[:len(stack)-1]

		id := cur.ID()
		if _, ok := cut[id]; ok {
			continue
		}
		if _, ok := introduced[id]; ok {
			continue
		}
		introduced[id] = introducedEntry{commit: cur, ref: ref}

		for _, parent := range cur.Parents() {
			if parent.IsZero() {
				continue
			}
			if _, ok := cut[parent]; ok {
				continue
			}
			if _, ok := introduced[parent]; ok {
				continue
			}
			next, err := p.repo.LookupCommit(ctx, parent)
			if err != nil {
				return fmt.Errorf("netsec: traverse push: %w", err)
			}
			stack = append(stack, next)
		}
	}
	return nil
}

func (p *Policy) objectError(update RefUpdate, id OID, cause error) error {
	return &AdmissionError{
		Update: update,
		Violations: []Violation{{
			Ref:    update.Ref,
			Commit: id,
			Err:    fmt.Errorf("%w: %v", ErrObjectNotFound, cause),
		}},
		Err: ErrObjectNotFound,
	}
}
