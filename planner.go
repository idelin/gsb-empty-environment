package netsec

import (
	"errors"
	"fmt"
	"sort"
)

// OpKind selects the kind of transaction operation.
type OpKind uint8

const (
	// OpMap creates a new mapping at an unused virtual page.
	OpMap OpKind = iota + 1
	// OpUnmap removes an existing mapping.
	OpUnmap
	// OpReprotect changes the permissions of an existing mapping.
	OpReprotect
)

func (k OpKind) String() string {
	switch k {
	case OpMap:
		return "map"
	case OpUnmap:
		return "unmap"
	case OpReprotect:
		return "reprotect"
	default:
		return fmt.Sprintf("opkind(%d)", int(k))
	}
}

// Op is one operation of a transaction.
//
// For OpMap, Phys and Perm describe the new mapping and Virt must be unused.
// For OpUnmap, only Virt is used and it must be mapped.
// For OpReprotect, Virt must be mapped and Perm is the new permission mask.
type Op struct {
	Kind OpKind
	Virt VirtPage
	Phys PhysPage
	Perm Perm
}

// Txn is an ordered batch of mapping operations. A virtual page may appear at
// most twice, and only as an Unmap immediately followed by a Map (remapping to
// another physical page).
type Txn struct {
	Ops []Op
}

// Phase classifies a driver step by whether it decreases or increases access.
type Phase uint8

const (
	// PhaseDowngrade removes access: unmap, or a strict subset of permissions.
	PhaseDowngrade Phase = iota + 1
	// PhaseUpgrade adds access: map, or a strict superset of permissions.
	PhaseUpgrade
)

func (p Phase) String() string {
	switch p {
	case PhaseDowngrade:
		return "downgrade"
	case PhaseUpgrade:
		return "upgrade"
	default:
		return fmt.Sprintf("phase(%d)", int(p))
	}
}

// Step is one concrete driver call. Within a phase, steps are ordered by
// virtual page so plans are deterministic.
type Step struct {
	Phase Phase
	Op    Op
	// Index is the zero-based position of the step inside its plan.
	Index int
}

func (s Step) String() string {
	switch s.Op.Kind {
	case OpUnmap:
		return fmt.Sprintf("#%d %s virt=%d unmap", s.Index, s.Phase, s.Op.Virt)
	case OpMap:
		return fmt.Sprintf("#%d %s virt=%d map -> phys %d %s", s.Index, s.Phase, s.Op.Virt, s.Op.Phys, s.Op.Perm)
	default:
		return fmt.Sprintf("#%d %s virt=%d reprotect %s", s.Index, s.Phase, s.Op.Virt, s.Op.Perm)
	}
}

// Plan is an ordered, invariant-safe sequence of driver steps.
type Plan struct {
	Steps []Step
	// From is the state the plan starts from.
	From *State
	// To is the state reached after every step succeeds.
	To *State
}

// IsEmpty reports whether the plan performs no work.
func (p *Plan) IsEmpty() bool { return len(p.Steps) == 0 }

// Downgrades returns the downgrade prefix of the plan.
func (p *Plan) Downgrades() []Step {
	var out []Step
	for _, st := range p.Steps {
		if st.Phase == PhaseDowngrade {
			out = append(out, st)
		}
	}
	return out
}

// Upgrades returns the upgrade suffix of the plan.
func (p *Plan) Upgrades() []Step {
	var out []Step
	for _, st := range p.Steps {
		if st.Phase == PhaseUpgrade {
			out = append(out, st)
		}
	}
	return out
}

// PlanningError explains why a transaction cannot be compiled into a safe plan.
type PlanningError struct {
	Op     Op
	Reason string
}

func (e *PlanningError) Error() string {
	return fmt.Sprintf("netsec: invalid operation (%s virt=%d): %s", e.Op.Kind, e.Op.Virt, e.Reason)
}

func planError(op Op, format string, args ...any) error {
	return &PlanningError{Op: op, Reason: fmt.Sprintf(format, args...)}
}

// simulate applies a transaction to a private copy of from and returns the
// resulting state. It validates preconditions, internal consistency and the
// W^X invariant of both the starting and resulting state.
func simulate(from *State, txn Txn) (*State, error) {
	if err := from.CheckInvariant(); err != nil {
		return nil, fmt.Errorf("netsec: starting state violates invariant: %w", err)
	}
	lastKind := make(map[VirtPage]OpKind, len(txn.Ops))
	to := from.Clone()
	for _, op := range txn.Ops {
		if prev, dup := lastKind[op.Virt]; dup {
			if !(prev == OpUnmap && op.Kind == OpMap) {
				return nil, planError(op, "virtual page %d touched more than once in transaction", op.Virt)
			}
		}
		lastKind[op.Virt] = op.Kind

		switch op.Kind {
		case OpMap:
			if _, exists := to.Mappings[op.Virt]; exists {
				return nil, planError(op, "virtual page %d is already mapped", op.Virt)
			}
			if op.Perm == 0 {
				return nil, planError(op, "map requires non-zero permissions")
			}
			to.Mappings[op.Virt] = Mapping{Virt: op.Virt, Phys: op.Phys, Perm: op.Perm}
		case OpUnmap:
			if _, exists := to.Mappings[op.Virt]; !exists {
				return nil, planError(op, "virtual page %d is not mapped", op.Virt)
			}
			delete(to.Mappings, op.Virt)
		case OpReprotect:
			cur, exists := to.Mappings[op.Virt]
			if !exists {
				return nil, planError(op, "virtual page %d is not mapped", op.Virt)
			}
			if op.Perm == 0 {
				return nil, planError(op, "reprotect requires non-zero permissions; use unmap to remove a mapping")
			}
			cur.Perm = op.Perm
			to.Mappings[op.Virt] = cur
		default:
			return nil, planError(op, "unknown operation kind %d", op.Kind)
		}
	}
	if err := to.CheckInvariant(); err != nil {
		return nil, fmt.Errorf("netsec: transaction target violates invariant: %w", err)
	}
	return to, nil
}

// diffSteps computes the driver steps needed to move from state from to state
// to. Both states must satisfy the invariant. Every downgrade step is emitted
// before every upgrade step.
func diffSteps(from, to *State) []Step {
	type classified struct {
		virt  VirtPage
		phase Phase
		op    Op
	}
	var items []classified

	// Removals and permission changes on currently mapped pages.
	for v, cur := range from.Mappings {
		next, ok := to.Mappings[v]
		switch {
		case !ok:
			items = append(items, classified{v, PhaseDowngrade, Op{Kind: OpUnmap, Virt: v}})
		case next.Phys != cur.Phys:
			// Moving an alias to another physical page: drop the old mapping
			// first (downgrade), then create the new one (upgrade).
			items = append(items, classified{v, PhaseDowngrade, Op{Kind: OpUnmap, Virt: v}})
			items = append(items, classified{v, PhaseUpgrade, Op{Kind: OpMap, Virt: v, Phys: next.Phys, Perm: next.Perm}})
		case next.Perm != cur.Perm:
			gained := next.Perm &^ cur.Perm
			lost := cur.Perm &^ next.Perm
			switch {
			case lost != 0 && gained == 0:
				items = append(items, classified{v, PhaseDowngrade, Op{Kind: OpReprotect, Virt: v, Perm: next.Perm}})
			case gained != 0 && lost == 0:
				items = append(items, classified{v, PhaseUpgrade, Op{Kind: OpReprotect, Virt: v, Perm: next.Perm}})
			default:
				// Mixed change: split into a strict downgrade to the common
				// mask followed by an upgrade to the target mask.
				common := cur.Perm & next.Perm
				if common != 0 {
					items = append(items, classified{v, PhaseDowngrade, Op{Kind: OpReprotect, Virt: v, Perm: common}})
					items = append(items, classified{v, PhaseUpgrade, Op{Kind: OpReprotect, Virt: v, Perm: next.Perm}})
				} else {
					items = append(items, classified{v, PhaseDowngrade, Op{Kind: OpUnmap, Virt: v}})
					items = append(items, classified{v, PhaseUpgrade, Op{Kind: OpMap, Virt: v, Phys: next.Phys, Perm: next.Perm}})
				}
			}
		}
	}

	// Newly created mappings.
	for v, next := range to.Mappings {
		if _, ok := from.Mappings[v]; !ok {
			items = append(items, classified{v, PhaseUpgrade, Op{Kind: OpMap, Virt: v, Phys: next.Phys, Perm: next.Perm}})
		}
	}

	sort.SliceStable(items, func(i, j int) bool {
		if items[i].phase != items[j].phase {
			return items[i].phase == PhaseDowngrade
		}
		return items[i].virt < items[j].virt
	})

	steps := make([]Step, len(items))
	for i, it := range items {
		steps[i] = Step{Phase: it.phase, Op: it.op, Index: i}
	}
	return steps
}

// PlanTxn compiles a mapping transaction into an invariant-safe step plan.
// It fails with *PlanningError when preconditions do not hold, or with the
// invariant violation when the resulting state would allow writing a physical
// page through one alias while executing it through another.
func PlanTxn(from *State, txn Txn) (*Plan, error) {
	if from == nil {
		return nil, errors.New("netsec: nil starting state")
	}
	to, err := simulate(from, txn)
	if err != nil {
		return nil, err
	}
	return &Plan{Steps: diffSteps(from, to), From: from.Clone(), To: to}, nil
}

// PlanStateDiff compiles the steps that turn state from into state to. Both
// snapshots must already satisfy the invariant; this is used to build recovery
// plans from the engine's last known good state.
func PlanStateDiff(from, to *State) (*Plan, error) {
	if from == nil || to == nil {
		return nil, errors.New("netsec: nil state")
	}
	if err := from.CheckInvariant(); err != nil {
		return nil, fmt.Errorf("netsec: starting state violates invariant: %w", err)
	}
	if err := to.CheckInvariant(); err != nil {
		return nil, fmt.Errorf("netsec: target state violates invariant: %w", err)
	}
	return &Plan{Steps: diffSteps(from, to), From: from.Clone(), To: to.Clone()}, nil
}
