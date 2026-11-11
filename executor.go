package netsec

import "errors"

// Reservation is one capacity reserve against a hardware domain.
type Reservation struct {
	DomainID string
	Purpose  string
	Amount   int
}

// Reserver is the host-provided capacity backend. The planning package only
// relies on two primitives so any hardware abstraction can implement them.
//
// Implementations must guarantee:
//   - Reserve atomically claims Amount units or fails without claiming any
//     part of that single reservation.
//   - Release is only invoked for reservations returned by a successful
//     Reserve call and must undo them.
type Reserver interface {
	Reserve(r Reservation) error
	Release(r Reservation) error
}

// Executor turns a Plan into physical capacity reservations.
type Executor struct {
	reserver Reserver
}

// NewExecutor builds an Executor backed by reserver.
func NewExecutor(reserver Reserver) *Executor {
	return &Executor{reserver: reserver}
}

var (
	errNilPlan            = errors.New("netsec: plan must not be nil")
	errAssignmentLength   = errors.New("netsec: plan assignment length does not match request count")
	errAssignmentDomain   = errors.New("netsec: plan references an out-of-range domain")
	errAssignmentNegative = errors.New("netsec: plan contains a negative domain assignment")
)

// Execute reserves every capacity unit required by plan against in.
//
// The operation is all-or-nothing: reservations are issued sequentially, and
// any failure triggers a rollback of every reservation already made so no
// capacity remains partially occupied. On success the returned reservations
// match the plan one-to-one per (domain, purpose) aggregate.
func (e *Executor) Execute(in Input, plan *Plan) ([]Reservation, error) {
	if err := validateExecution(in, plan); err != nil {
		return nil, err
	}

	reservations := buildReservations(in, plan)
	committed := make([]Reservation, 0, len(reservations))

	for _, r := range reservations {
		if err := e.reserver.Reserve(r); err != nil {
			for rollbackIndex := len(committed) - 1; rollbackIndex >= 0; rollbackIndex-- {
				_ = e.reserver.Release(committed[rollbackIndex])
			}
			return nil, err
		}
		committed = append(committed, r)
	}
	return committed, nil
}

func validateExecution(in Input, plan *Plan) error {
	if plan == nil {
		return errNilPlan
	}
	if len(plan.Assignment) != len(in.Requests) {
		return errAssignmentLength
	}
	for _, d := range plan.Assignment {
		if d < 0 {
			return errAssignmentNegative
		}
		if d >= len(in.Domains) {
			return errAssignmentDomain
		}
	}
	return nil
}

// buildReservations aggregates request demand per (domain, purpose) so each
// capacity pool is reserved once.
func buildReservations(in Input, plan *Plan) []Reservation {
	type key struct {
		domain  int
		purpose string
	}
	amounts := make(map[key]int)
	order := make([]key, 0)

	for reqIndex, domainIndex := range plan.Assignment {
		k := key{domain: domainIndex, purpose: in.Requests[reqIndex].KeyPurpose}
		if _, seen := amounts[k]; !seen {
			order = append(order, k)
		}
		amounts[k] += in.Requests[reqIndex].Capacity
	}

	out := make([]Reservation, 0, len(order))
	for _, k := range order {
		out = append(out, Reservation{
			DomainID: in.Domains[k.domain].ID,
			Purpose:  k.purpose,
			Amount:   amounts[k],
		})
	}
	return out
}
