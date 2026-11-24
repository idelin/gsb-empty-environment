// Package zplanner computes exact-optimal, fault-free commit plans for a
// zoned, append-only block device that keeps logical state in dual root
// table slots.
//
// The planner works on abstract state only: it has no dependency on the
// simulated device. Costs are compared lexicographically: internal copied
// pages first, zone resets second.
package zplanner

const (
	// MaxZones is the maximum number of zones on the device.
	MaxZones = 5
	// PagesPerZone is the fixed capacity of one zone in pages.
	PagesPerZone = 8
	// MaxCIDs bounds the number of distinct physical content IDs.
	MaxCIDs = 8
	// MaxOps bounds the number of client operations in one sequence.
	MaxOps = 6
)

// CID is an immutable content identifier. The same CID may be referenced by
// the active roots and any number of named snapshots simultaneously.
type CID uint8

// ZoneID identifies a device zone in [0, MaxZones).
type ZoneID uint8

// Phys is the physical location of one page.
type Phys struct {
	Zone ZoneID
	Off  uint8
}

// OpKind enumerates the ordered client operations.
type OpKind uint8

const (
	// OpWrite atomically rewrites a group of logical pages.
	OpWrite OpKind = iota + 1
	// OpSnapshot captures the current logical-to-CID mapping under a name.
	OpSnapshot
	// OpDeleteSnapshot removes a named snapshot.
	OpDeleteSnapshot
)

// Op is one ordered client operation. ID is the durable, client-supplied
// sequence identifier used for idempotent redelivery.
type Op struct {
	ID     int
	Kind   OpKind
	Name   string
	Writes map[string]CID
}

// Table is the content of one root table slot. The root table itself only
// maps content IDs to physical locations; roots and snapshots reference IDs
// indirectly, which lets one physical copy back several references.
type Table struct {
	Generation int
	// LastOp is the ID of the last client operation reflected by this table.
	LastOp int
	Loc    map[CID]Phys
	Roots  map[string]CID
	Snaps  map[string]map[string]CID
}

// Clone returns a deep copy of the table.
func (t Table) Clone() Table {
	out := Table{
		Generation: t.Generation,
		LastOp:     t.LastOp,
		Loc:        make(map[CID]Phys, len(t.Loc)),
		Roots:      make(map[string]CID, len(t.Roots)),
		Snaps:      make(map[string]map[string]CID, len(t.Snaps)),
	}
	for k, v := range t.Loc {
		out.Loc[k] = v
	}
	for k, v := range t.Roots {
		out.Roots[k] = v
	}
	for name, snap := range t.Snaps {
		cp := make(map[string]CID, len(snap))
		for k, v := range snap {
			cp[k] = v
		}
		out.Snaps[name] = cp
	}
	return out
}

// StepKind enumerates the executable steps of a plan.
type StepKind uint8

const (
	// StepWrite appends a brand-new client content page. It consumes capacity
	// but has no optimization cost.
	StepWrite StepKind = iota + 1
	// StepCopy appends an internal relocation copy of a live CID.
	StepCopy
	// StepReset erases a zone that no bootable slot references.
	StepReset
	// StepPublish atomically replaces the root table slot and acknowledges it.
	StepPublish
)

// Step is one executable plan action.
type Step struct {
	Kind StepKind
	// CID is the content written or copied (StepWrite, StepCopy).
	CID CID
	// Zone is the target zone (StepWrite, StepCopy, StepReset).
	Zone ZoneID
	// OpID is the client operation for a client publish; -1 for relocation
	// publishes.
	OpID int
	// Table is the full slot image for StepPublish.
	Table *Table
}

// Cost is the lexicographic optimization objective.
type Cost struct {
	Copies int
	Erases int
}

// Less reports whether a is strictly better than b: copies dominate, then
// erases.
func (a Cost) Less(b Cost) bool {
	if a.Copies != b.Copies {
		return a.Copies < b.Copies
	}
	return a.Erases < b.Erases
}

// CommitPlan is an ordered, fault-free execution plan.
type CommitPlan struct {
	Steps []Step
	Cost  Cost
}

// InitialState describes the device state the planner starts from. Fill
// reports the current append offset of every zone, including unreferenced
// garbage pages left behind by a crash.
type InitialState struct {
	Fill  [MaxZones]int
	Table Table
}

// ErrInfeasible is returned when no legal plan can complete the operation
// sequence within the device geometry.
type ErrInfeasible struct{ reason string }

func (e *ErrInfeasible) Error() string { return "zplanner: infeasible: " + e.reason }
