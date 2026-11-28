// Package fedscheduler implements federated replica scheduling primitives
// for a Karmada-style multi-cluster control plane:
//
//   - capped weighted apportionment with recursive overflow redistribution
//     and deterministic largest-remainder tie breaking;
//   - Region/Zone spread constraints (per-domain replica ranges and minimum
//     domain coverage);
//   - KeepExisting minimal-disruption rebalancing with a provably minimal
//     number of evicted (migrated) replicas;
//   - precise insufficient-capacity diagnostics carrying the maximum
//     schedulable plan.
package fedscheduler

import (
	"errors"
	"fmt"
)

// ErrInsufficientCapacity is the sentinel matched by errors.Is when the
// aggregate hard capacity of the candidate clusters is below the request.
var ErrInsufficientCapacity = errors.New("fedscheduler: insufficient aggregate cluster capacity")

// ErrInvalidInput is the sentinel matched by errors.Is for malformed
// scheduling requests.
var ErrInvalidInput = errors.New("fedscheduler: invalid scheduling input")

// InsufficientCapacityError reports that the requested replica count cannot
// be placed under the hard per-cluster capacities. MaxPlan holds the largest
// feasible allocation (every cluster saturated at its capacity), which is
// also the unique maximum-sum plan.
type InsufficientCapacityError struct {
	// Requested is the replica count the caller asked for.
	Requested int64
	// Capacity is the aggregate hard capacity across all candidate clusters.
	Capacity int64
	// Deficit is Requested - Capacity.
	Deficit int64
	// MaxPlan maps cluster name to the maximum replicas it can take.
	MaxPlan map[string]int64
}

func (e *InsufficientCapacityError) Error() string {
	return fmt.Sprintf(
		"fedscheduler: insufficient capacity: requested %d replicas, aggregate capacity %d, deficit %d",
		e.Requested, e.Capacity, e.Deficit,
	)
}

// Is enables errors.Is(err, ErrInsufficientCapacity).
func (e *InsufficientCapacityError) Is(target error) bool {
	return target == ErrInsufficientCapacity
}

// InvalidInputError describes a malformed request (negative replicas,
// duplicate cluster names, non-positive weights, infeasible constraints, ...).
type InvalidInputError struct {
	// Reason explains the validation failure.
	Reason string
}

func (e *InvalidInputError) Error() string {
	return "fedscheduler: invalid input: " + e.Reason
}

// Is enables errors.Is(err, ErrInvalidInput).
func (e *InvalidInputError) Is(target error) bool {
	return target == ErrInvalidInput
}

func invalidInput(format string, args ...any) error {
	return &InvalidInputError{Reason: fmt.Sprintf(format, args...)}
}
