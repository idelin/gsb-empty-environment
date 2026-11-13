package netsec

// PhysicalPageCount is the number of physical pages covered by every layout.
const PhysicalPageCount = 4096

// The repair search deliberately bounds the useful relocation universe.  A
// witness contains three pages, so a single move can affect many witnesses,
// but the stated host budget is at most twenty movable pages and twenty free
// pages.
const (
	MaxMovablePages = 20
	MaxFreePages    = 20

	DefaultSearchBudget = 1_000_000
)

// PageID is the stable identity of a page.  It is independent of the physical
// page currently holding the page's contents.
type PageID = int

// PhysicalPage is an index in [0, PhysicalPageCount).
type PhysicalPage = int

// TenantID identifies the trust domain owning a page.  The adjacency model
// does not privilege or restrict particular tenant names: any attacker-role
// page is eligible, regardless of which tenant owns it.
type TenantID = string

// Role describes a page's participation in the adjacency model.
type Role uint8

const (
	// RoleOrdinary pages participate in neither side nor centre of a witness.
	RoleOrdinary Role = iota
	// RoleAttacker pages can occupy either side row of a witness.
	RoleAttacker
	// RoleSensitive pages can occupy only the centre row.
	RoleSensitive
)

// Short aliases make literal layout tables less noisy.
const (
	Ordinary  = RoleOrdinary
	Attacker  = RoleAttacker
	Sensitive = RoleSensitive
)

func (r Role) String() string {
	switch r {
	case RoleOrdinary:
		return "ordinary"
	case RoleAttacker:
		return "attacker"
	case RoleSensitive:
		return "sensitive"
	default:
		return "invalid"
	}
}

// Limits defines the finite address space returned by Layout.Locate.  A
// successful mapping must satisfy 0 <= Channel < Channels, 0 <= Bank < Banks,
// and 0 <= Row < Rows.
type Limits struct {
	Channels int
	Banks    int
	Rows     int
}

// Location is the decoded DRAM geometry of one physical page.
type Location struct {
	Channel int
	Bank    int
	Row     int
}

// Page is one page of the initial layout.  Physical identifies the page frame
// currently holding the page contents.  IDs and physical frames must each be
// unique.
type Page struct {
	ID       PageID
	Tenant   TenantID
	Role     Role
	Physical PhysicalPage
	Movable  bool
}

// Layout is the complete input to audit and repair.  Locate must be a pure
// function.  Returning false denotes an address that is not mapped; a
// successful result outside Limits is a validation error.
//
// FreePages are the only legal relocation destinations.  They must be
// unoccupied in the initial layout and are consumed one-to-one.
type Layout struct {
	Pages     []Page
	FreePages []PhysicalPage
	Limits    Limits
	Locate    func(PhysicalPage) (Location, bool)
}

// Witness is an ordered sensitive/attacker/attacker triple.  AttackerBelow is
// at Row-1 and AttackerAbove is at Row+1 in the same channel and bank as the
// sensitive page.  The three page IDs are distinct by construction.
//
// Witnesses are sorted by (Sensitive, AttackerBelow, AttackerAbove).
type Witness struct {
	Sensitive             PageID
	AttackerBelow         PageID
	AttackerAbove         PageID
	Channel               int
	Bank                  int
	Row                   int
	SensitivePhysical     PhysicalPage
	AttackerBelowPhysical PhysicalPage
	AttackerAbovePhysical PhysicalPage
}

// Report is the complete minimal-cardinality witness enumeration.  Every
// returned triple is itself a witness; no witness has fewer than three pages.
type Report struct {
	Witnesses     []Witness
	WitnessCount  int64
	MappedPages   int
	UnmappedPages int
}

// Move records one page relocation.  From is the page's initial physical
// frame and To is a page from Layout.FreePages.
type Move struct {
	Page PageID
	From PhysicalPage
	To   PhysicalPage
}

// RepairOptions controls the bounded exact search.  A Budget of zero uses
// DefaultSearchBudget.  The budget counts visited search states, including the
// states visited while checking online orderings.
type RepairOptions struct {
	Budget int
}

// RepairResult reports both the final one-to-one assignment and, when
// possible, an ordering of those moves that never adds a witness triple.
//
// Moves is canonical: sorted by Page ID.  Sequence is the ordered form and
// Counts contains the witness count before the first move and after each move.
// If no safe online order exists, Sequence is empty and Online is false.
type RepairResult struct {
	InitialWitnesses []Witness
	InitialCount     int64

	Moves      []Move
	Sequence   []Move
	Counts     []int64
	MoveCount  int
	FinalCount int64

	// LowerBound is a proved lower bound on the number of moved pages for a
	// final witness-free layout.  On an exhaustive successful result it equals
	// MoveCount.
	LowerBound int
	// StaticOptimal means the minimum static move count was proved.
	StaticOptimal bool
	// Optimal means the returned online-safe plan was proved minimum.
	Optimal bool
	// Exhausted means the state budget was reached before the search closed.
	Exhausted bool
	// Online means Sequence is a legal strictly non-expanding witness order.
	Online bool
	// StaticSafe means Moves (possibly empty) is a witness-free assignment.
	StaticSafe bool
}
