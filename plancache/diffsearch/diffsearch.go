// Package diffsearch implements a bounded differential searcher over
// candidate cacheKey constructors under review.
//
// The search space is a grid in which six security dimensions each have two
// states:
//
//	principal, role closure, nested-view version, object schema version,
//	RLS policy digest, global policy epoch.
//
// For every pair of grid states within a bounded Hamming distance, the
// searcher asks (1) whether the candidate constructor produces a key
// collision and (2) whether the authorization outcomes differ. A pair where
// a collision coexists with differing authorization is a weak-key
// counterexample. The shortest such counterexample is reported first.
package diffsearch

import (
	"sort"
	"strconv"
	"strings"

	"formal.local/w3-bc80ec5616/plancache"
)

// Dimension identifies one of the six two-state security axes.
type Dimension int

const (
	Principal Dimension = iota
	RoleClosure
	ViewVersion
	ObjectVersion
	RLSDigest
	PolicyEpoch
)

// AllDimensions is the fixed six-dimensional grid definition.
var AllDimensions = []Dimension{
	Principal,
	RoleClosure,
	ViewVersion,
	ObjectVersion,
	RLSDigest,
	PolicyEpoch,
}

// GridState is one point of the 2^6 grid. Each field is the binary state of
// the corresponding dimension (0 = baseline, 1 = changed/revoked).
type GridState struct {
	Principal     int
	RoleClosure   int
	ViewVersion   int
	ObjectVersion int
	RLSDigest     int
	PolicyEpoch   int
}

// Get reads one dimension's binary state.
func (g GridState) Get(d Dimension) int {
	switch d {
	case Principal:
		return g.Principal
	case RoleClosure:
		return g.RoleClosure
	case ViewVersion:
		return g.ViewVersion
	case ObjectVersion:
		return g.ObjectVersion
	case RLSDigest:
		return g.RLSDigest
	case PolicyEpoch:
		return g.PolicyEpoch
	default:
		panic("diffsearch: unknown dimension")
	}
}

// With returns a copy with one dimension flipped to the given binary state.
func (g GridState) With(d Dimension, state int) GridState {
	switch d {
	case Principal:
		g.Principal = state
	case RoleClosure:
		g.RoleClosure = state
	case ViewVersion:
		g.ViewVersion = state
	case ObjectVersion:
		g.ObjectVersion = state
	case RLSDigest:
		g.RLSDigest = state
	case PolicyEpoch:
		g.PolicyEpoch = state
	}
	return g
}

// Hamming returns the number of dimensions on which two states differ.
func (g GridState) Hamming(other GridState) int {
	dist := 0
	for _, d := range AllDimensions {
		if g.Get(d) != other.Get(d) {
			dist++
		}
	}
	return dist
}

// DiffDimensions returns the dimensions on which the states differ, in the
// canonical AllDimensions order.
func (g GridState) DiffDimensions(other GridState) []Dimension {
	var out []Dimension
	for _, d := range AllDimensions {
		if g.Get(d) != other.Get(d) {
			out = append(out, d)
		}
	}
	return out
}

// Names maps dimensions to stable names for reports.
var Names = map[Dimension]string{
	Principal:     "principal",
	RoleClosure:   "roleClosure",
	ViewVersion:   "viewDefVersion",
	ObjectVersion: "objectSchemaVersion",
	RLSDigest:     "rlsPolicyDigest",
	PolicyEpoch:   "policyEpoch",
}

// ToCompileInput maps a grid state onto a concrete, internally consistent
// plancache.CompileInput. The baseline state (all zeros) is authorized;
// flipping a dimension models the corresponding revocation/rewrite.
func ToCompileInput(g GridState) plancache.CompileInput {
	caller := plancache.PrincipalID("alice")
	if g.Principal == 1 {
		caller = plancache.PrincipalID("mallory")
	}

	roles := plancache.Digest("roles:alice:v1")
	if g.RoleClosure == 1 {
		roles = plancache.Digest("roles:alice:v2-revoked")
	}

	viewVersion := int64(1 + g.ViewVersion)
	schemaVersion := int64(1 + g.ObjectVersion)
	rls := plancache.Digest("rls:allow-all")
	if g.RLSDigest == 1 {
		rls = plancache.Digest("rls:deny-tenant-cross")
	}
	epoch := int64(1 + g.PolicyEpoch)

	oid := plancache.ObjectID("obj-orders-1")
	vid := plancache.ViewID("view-v_orders-1")

	return plancache.CompileInput{
		ASTDigest:   plancache.Digest("ast:canonical:select-orders"),
		ParamTypes:  []string{"int64"},
		Caller:      caller,
		RoleClosure: roles,
		PolicyEpoch: epoch,
		RequiredOps: []plancache.Operation{"SELECT"},
		DepGraph: []plancache.DependencyNode{
			{
				RefType: "view",
				View: plancache.ViewRef{
					ViewID:          vid,
					ViewDefVersion:  viewVersion,
					UnderlyingTable: oid,
				},
			},
			{
				RefType: "table",
				Table: plancache.TableRef{
					ObjectID:      oid,
					SchemaVersion: schemaVersion,
					RLSPolicy:     rls,
				},
			},
		},
		RLSPolicies: map[plancache.ObjectID]plancache.Digest{oid: rls},
	}
}

// KeyConstructor is the cacheKey builder under review.
type KeyConstructor interface {
	// Name identifies the constructor in reports.
	Name() string
	// BuildKey returns the string identity the constructor assigns.
	BuildKey(in plancache.CompileInput) string
}

// AuthzOracle decides authorization for a grid state. The baseline state is
// allowed; any single flip encodes a security-relevant change that must
// change the key, and a flip of principal/role/RLS/epoch also changes the
// authorization outcome. Object/view version flips alone invalidate a plan
// (recompile) but a fresh recompile of an otherwise-authorized principal is
// allowed, so they are modeled as an authorization difference only when
// combined with a privilege-bearing dimension — handled by the
// DefaultOracle rules below.
type AuthzOracle func(g GridState) bool

// DefaultOracle grants only the all-zero baseline: every other grid point
// represents a withdrawn or rewritten state that must not share a key with
// the baseline plan.
func DefaultOracle(g GridState) bool {
	for _, d := range AllDimensions {
		if g.Get(d) != 0 {
			return false
		}
	}
	return true
}

// Counterexample is a shortest weak-key collision with differing
// authorization.
type Counterexample struct {
	Constructor string
	StateA      GridState
	StateB      GridState
	KeyHash     string
	AuthzA      bool
	AuthzB      bool
	DifferOn    []Dimension
	Distance    int
}

// Result is the bounded search outcome for one constructor.
type Result struct {
	Constructor    string
	Counterexample *Counterexample
	PairsExamined  int
	Bound          int
}

// Search runs a bounded differential search for one constructor.
//
// maxDistance bounds the Hamming distance between the two grid states of a
// candidate pair (1..6). Pairs are examined in increasing distance, and
// within one distance in deterministic (sorted) order, so the first hit is
// the shortest counterexample with a stable tie-break.
func Search(c KeyConstructor, oracle AuthzOracle, maxDistance int) Result {
	if maxDistance < 1 {
		maxDistance = 1
	}
	if maxDistance > len(AllDimensions) {
		maxDistance = len(AllDimensions)
	}

	states := allGridStates()
	examined := 0

	for dist := 1; dist <= maxDistance; dist++ {
		pairs := pairsAtDistance(states, dist)
		for _, pair := range pairs {
			examined++
			a, b := pair[0], pair[1]
			inA := ToCompileInput(a)
			inB := ToCompileInput(b)
			keyA := c.BuildKey(inA)
			keyB := c.BuildKey(inB)
			if keyA != keyB {
				continue
			}
			authA := oracle(a)
			authB := oracle(b)
			if authA == authB {
				continue
			}
			diffs := a.DiffDimensions(b)
			ce := &Counterexample{
				Constructor: c.Name(),
				StateA:      a,
				StateB:      b,
				KeyHash:     keyA,
				AuthzA:      authA,
				AuthzB:      authB,
				DifferOn:    diffs,
				Distance:    dist,
			}
			return Result{
				Constructor:    c.Name(),
				Counterexample: ce,
				PairsExamined:  examined,
				Bound:          maxDistance,
			}
		}
	}
	return Result{Constructor: c.Name(), PairsExamined: examined, Bound: maxDistance}
}

// SearchAll runs Search over several constructors and returns every result.
func SearchAll(constructors []KeyConstructor, oracle AuthzOracle, maxDistance int) []Result {
	out := make([]Result, 0, len(constructors))
	for _, c := range constructors {
		out = append(out, Search(c, oracle, maxDistance))
	}
	return out
}

func allGridStates() []GridState {
	n := len(AllDimensions)
	total := 1 << n
	out := make([]GridState, 0, total)
	for mask := 0; mask < total; mask++ {
		g := GridState{}
		for i, d := range AllDimensions {
			if mask&(1<<i) != 0 {
				g = g.With(d, 1)
			}
		}
		out = append(out, g)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].less(out[j]) })
	return out
}

func pairsAtDistance(states []GridState, dist int) [][2]GridState {
	var out [][2]GridState
	for i := 0; i < len(states); i++ {
		for j := i + 1; j < len(states); j++ {
			if states[i].Hamming(states[j]) == dist {
				out = append(out, [2]GridState{states[i], states[j]})
			}
		}
	}
	return out
}

func (g GridState) less(other GridState) bool {
	for _, d := range AllDimensions {
		if g.Get(d) != other.Get(d) {
			return g.Get(d) < other.Get(d)
		}
	}
	return false
}

// String renders a counterexample as a compact human-readable report.
func (c *Counterexample) String() string {
	if c == nil {
		return "no counterexample"
	}
	dims := make([]string, 0, len(c.DifferOn))
	for _, d := range c.DifferOn {
		dims = append(dims, Names[d])
	}
	return strings.Join([]string{
		"constructor=" + c.Constructor,
		"distance=" + strconv.Itoa(c.Distance),
		"collisionKey=" + c.KeyHash,
		"authzA=" + strconv.FormatBool(c.AuthzA),
		"authzB=" + strconv.FormatBool(c.AuthzB),
		"differOn=" + strings.Join(dims, ","),
	}, " ")
}
