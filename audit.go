package netsec

import (
	"fmt"
	"strings"
)

// AuditAction is one step in the executable migration model used by the
// shortest-witness search.
type AuditAction string

const (
	AuditWrite       AuditAction = "write"
	AuditSnapshot    AuditAction = "round.snapshot"
	AuditAcknowledge AuditAction = "dirty.ack"
	AuditFreeze      AuditAction = "freeze.final"
	AuditStart       AuditAction = "target.start"
)

// AuditPolicy selects the implementation behavior under review.  A safe
// implementation sets every field to false.
type AuditPolicy struct {
	// ClearDirtyIgnoresGeneration models the historical bug: a copy
	// acknowledgement clears dirty without rechecking the source generation.
	ClearDirtyIgnoresGeneration bool
	// FreezeSkipsDirtyCopy models a cutover that builds a manifest while
	// forgetting to transfer the final dirty pages.
	FreezeSkipsDirtyCopy bool
	// StartWithoutGenerationCheck models a target that trusts its quarantine
	// without comparing every page generation to the frozen manifest.
	StartWithoutGenerationCheck bool
}

// SafeAuditPolicy is the specified implementation.
func SafeAuditPolicy() AuditPolicy { return AuditPolicy{} }

// LegacyUnsafeAuditPolicy reproduces the dirty-bit-clearing bug combined with
// a target that does not verify page generations at startup.
func LegacyUnsafeAuditPolicy() AuditPolicy {
	return AuditPolicy{
		ClearDirtyIgnoresGeneration: true,
		StartWithoutGenerationCheck: true,
	}
}

// AuditState is the complete state of the one-page executable model.
type AuditState struct {
	SourceGeneration   Generation `json:"source_generation"`
	Dirty              bool       `json:"dirty"`
	SnapshotValid      bool       `json:"snapshot_valid"`
	SnapshotGeneration Generation `json:"snapshot_generation"`
	SnapshotDirty      bool       `json:"snapshot_dirty"`
	TargetValid        bool       `json:"target_valid"`
	TargetGeneration   Generation `json:"target_generation"`
	Frozen             bool       `json:"frozen"`
	Finalized          bool       `json:"finalized"`
	FreezeGeneration   Generation `json:"freeze_generation"`
	Started            bool       `json:"started"`
}

// AuditStep is an action and the state produced by it.
type AuditStep struct {
	Action AuditAction `json:"action"`
	State  AuditState  `json:"state"`
}

// AuditWitness is the shortest action sequence that starts a target with a
// page generation below the source's freeze point.
type AuditWitness struct {
	Steps                   []AuditStep `json:"steps"`
	Page                    PageID      `json:"page"`
	SourceFreezeGeneration  Generation  `json:"source_freeze_generation"`
	TargetStartupGeneration Generation  `json:"target_startup_generation"`
}

// ActionSequence returns a compact human-readable witness.
func (w AuditWitness) ActionSequence() string {
	parts := make([]string, len(w.Steps))
	for i, s := range w.Steps {
		parts[i] = string(s.Action)
	}
	return strings.Join(parts, " -> ")
}

// String renders the witness with the exact stale generation pair.
func (w AuditWitness) String() string {
	return fmt.Sprintf("%s (page %d starts at generation %d, source froze at %d)",
		w.ActionSequence(), w.Page, w.TargetStartupGeneration, w.SourceFreezeGeneration)
}

// FindShortestStaleStartWitness breadth-first searches the policy's executable
// action model.  Because BFS expands actions in a fixed order and never
// revisits a state, the first violation found is a shortest witness.
//
// The result is nil and ok is false for the safe policy (or any policy that
// cannot produce a stale visible page) within maxDepth.
func FindShortestStaleStartWitness(policy AuditPolicy, maxDepth int) (AuditWitness, bool) {
	if maxDepth < 1 {
		return AuditWitness{}, false
	}
	type node struct {
		state AuditState
		steps []AuditStep
	}
	start := AuditState{}
	queue := []node{{state: start}}
	visited := map[AuditState]bool{start: true}
	actions := []AuditAction{AuditWrite, AuditSnapshot, AuditAcknowledge, AuditFreeze, AuditStart}

	for depth := 0; depth < maxDepth; depth++ {
		if len(queue) == 0 {
			break
		}
		var next []node
		for _, n := range queue {
			for _, action := range actions {
				ns, ok, stale := auditApply(n.state, action, policy)
				if !ok {
					continue
				}
				step := AuditStep{Action: action, State: ns}
				steps := append(append([]AuditStep(nil), n.steps...), step)
				if stale {
					return AuditWitness{
						Steps:                   steps,
						Page:                    0,
						SourceFreezeGeneration:  ns.FreezeGeneration,
						TargetStartupGeneration: ns.TargetGeneration,
					}, true
				}
				if !visited[ns] {
					visited[ns] = true
					next = append(next, node{state: ns, steps: steps})
				}
			}
		}
		queue = next
	}
	return AuditWitness{}, false
}

// auditApply executes one action.  stale is true only for the transition that
// makes a valid target page visible below the source freeze generation.
func auditApply(s AuditState, a AuditAction, p AuditPolicy) (AuditState, bool, bool) {
	switch a {
	case AuditWrite:
		if s.Frozen || s.Started {
			return s, false, false
		}
		if s.SourceGeneration >= 5 {
			return s, false, false
		}
		s.SourceGeneration++
		s.Dirty = true
		return s, true, false

	case AuditSnapshot:
		if s.Frozen || s.Started {
			return s, false, false
		}
		s.SnapshotValid = true
		s.SnapshotGeneration = s.SourceGeneration
		s.SnapshotDirty = s.Dirty
		return s, true, false

	case AuditAcknowledge:
		if !s.SnapshotValid || s.Frozen || s.Started {
			return s, false, false
		}
		// The page is copied from the snapshot.  Only a dirty snapshot page
		// needs a target transfer.
		if s.SnapshotDirty && s.SnapshotGeneration > 0 {
			s.TargetValid = true
			s.TargetGeneration = s.SnapshotGeneration
			if p.ClearDirtyIgnoresGeneration || s.SourceGeneration == s.SnapshotGeneration {
				s.Dirty = false
			}
		}
		s.SnapshotValid = false
		return s, true, false

	case AuditFreeze:
		if s.Frozen || s.Started || s.SnapshotValid {
			return s, false, false
		}
		s.Frozen = true
		s.Finalized = true
		s.FreezeGeneration = s.SourceGeneration
		if !p.FreezeSkipsDirtyCopy && s.Dirty {
			s.TargetValid = true
			s.TargetGeneration = s.SourceGeneration
			s.Dirty = false
		}
		return s, true, false

	case AuditStart:
		if !s.Finalized || s.Started || !s.TargetValid {
			return s, false, false
		}
		stale := s.TargetGeneration < s.FreezeGeneration
		if stale && !p.StartWithoutGenerationCheck {
			return s, false, false
		}
		s.Started = true
		return s, true, stale
	}
	return s, false, false
}
