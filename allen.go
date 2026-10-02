// Package tempsched implements a temporal constraint network solver based on
// Allen's interval algebra, combined with a simple temporal network (STN)
// layer that computes earliest/latest feasible time windows for every task.
package tempsched

import "strings"

// pointRel is a set of possible orderings between two time points,
// encoded as a bitmask of lessThan | equal | greaterThan.
type pointRel uint8

const (
	relLT pointRel = 1 << iota
	relEQ
	relGT
	relAny = relLT | relEQ | relGT
)

// composePoint composes two point-relation sets: given a(x,y) and b(y,z)
// it returns the possible relations between x and z.
func composePoint(a, b pointRel) pointRel {
	var out pointRel
	for abit := pointRel(1); abit <= relGT; abit <<= 1 {
		if a&abit == 0 {
			continue
		}
		for bbit := pointRel(1); bbit <= relGT; bbit <<= 1 {
			if b&bbit == 0 {
				continue
			}
			out |= composePointSingle(abit, bbit)
		}
	}
	return out
}

func composePointSingle(a, b pointRel) pointRel {
	switch {
	case a == relEQ:
		return b
	case b == relEQ:
		return a
	case a == b: // <∘< = <, >∘> = >
		return a
	default: // <∘> or >∘< : anything is possible
		return relAny
	}
}

// Relation is a set of atomic Allen interval relations, encoded as a bitmask.
// An atomic value (single bit) denotes a definite relation; a set denotes a
// disjunctive constraint ("any of these relations may hold").
type Relation uint16

const (
	Before Relation = 1 << iota
	Meets
	Overlaps
	Starts
	During
	Finishes
	Equal
	After        // Before inverse
	MetBy        // Meets inverse
	OverlappedBy // Overlaps inverse
	StartedBy    // Starts inverse
	Contains     // During inverse
	FinishedBy   // Finishes inverse

	// Universal is the unconstrained relation (all 13 atomics).
	Universal Relation = 1<<13 - 1
)

var atomicNames = [13]string{
	"Before", "Meets", "Overlaps", "Starts", "During", "Finishes", "Equal",
	"After", "MetBy", "OverlappedBy", "StartedBy", "Contains", "FinishedBy",
}

// allenTuples encodes each atomic relation Ra as the four endpoint
// comparisons [cmp(sX,sY), cmp(sX,eY), cmp(eX,sY), cmp(eX,eY)].
var allenTuples = [13][4]pointRel{
	{relLT, relLT, relLT, relLT}, // Before:    eX < sY
	{relLT, relLT, relEQ, relLT}, // Meets:     eX = sY
	{relLT, relLT, relGT, relLT}, // Overlaps:  sX < sY < eX < eY
	{relEQ, relLT, relGT, relLT}, // Starts:    sX = sY, eX < eY
	{relGT, relLT, relGT, relLT}, // During:    sY < sX, eX < eY
	{relGT, relLT, relGT, relEQ}, // Finishes:  sY < sX, eX = eY
	{relEQ, relLT, relGT, relEQ}, // Equal
	{relGT, relGT, relGT, relGT}, // After
	{relGT, relEQ, relGT, relGT}, // MetBy:     eY = sX
	{relGT, relLT, relGT, relGT}, // OverlappedBy
	{relEQ, relLT, relGT, relGT}, // StartedBy
	{relLT, relLT, relLT, relGT}, // Contains
	{relLT, relLT, relLT, relEQ}, // FinishedBy
}

// inverseIndex maps each atomic relation to its converse.
var inverseIndex = [13]int{7, 8, 9, 10, 11, 12, 6, 0, 1, 2, 3, 4, 5}

// Inverse returns the converse of every atomic relation in r.
func Inverse(r Relation) Relation {
	var out Relation
	for i := 0; i < 13; i++ {
		if r&(1<<uint(i)) != 0 {
			out |= 1 << uint(inverseIndex[i])
		}
	}
	return out
}

// IsAtomic reports whether r is a single definite relation.
func IsAtomic(r Relation) bool {
	return r != 0 && r&(r-1) == 0
}

func (r Relation) String() string {
	if r == 0 {
		return "Inconsistent"
	}
	var parts []string
	for i := 0; i < 13; i++ {
		if r&(1<<uint(i)) != 0 {
			parts = append(parts, atomicNames[i])
		}
	}
	return strings.Join(parts, "|")
}

// composeTable is the 13x13 Allen composition table, derived at init time
// from the endpoint encodings instead of being hard-coded.
var composeTable [13][13]Relation

func init() {
	for i := 0; i < 13; i++ {
		for j := 0; j < 13; j++ {
			composeTable[i][j] = composeAtomic(i, j)
		}
	}
}

// composeAtomic derives the composition of atomic relations a(X,Y) and b(Y,Z)
// by composing endpoint relations through the shared interval Y and
// intersecting the two possible paths (via sY and via eY).
func composeAtomic(a, b int) Relation {
	ta, tb := allenTuples[a], allenTuples[b]
	var out [4]pointRel
	for xi := 0; xi < 2; xi++ { // endpoint of X: 0=start, 1=end
		for zj := 0; zj < 2; zj++ { // endpoint of Z
			r := relAny
			for yk := 0; yk < 2; yk++ { // intermediate endpoint of Y
				r &= composePoint(ta[2*xi+yk], tb[2*yk+zj])
			}
			out[2*xi+zj] = r
		}
	}
	var rel Relation
	for k := 0; k < 13; k++ {
		match := true
		for c := 0; c < 4; c++ {
			if out[c]&allenTuples[k][c] == 0 {
				match = false
				break
			}
		}
		if match {
			rel |= 1 << uint(k)
		}
	}
	return rel
}

// Compose returns the transitive composition a∘b of two relation sets.
func Compose(a, b Relation) Relation {
	var out Relation
	for i := 0; i < 13; i++ {
		if a&(1<<uint(i)) == 0 {
			continue
		}
		for j := 0; j < 13; j++ {
			if b&(1<<uint(j)) != 0 {
				out |= composeTable[i][j]
			}
		}
	}
	return out
}

// Network is an Allen interval algebra constraint network over named tasks.
type Network struct {
	names []string
	index map[string]int
	c     [][]Relation
}

// NewNetwork creates an empty constraint network.
func NewNetwork() *Network {
	return &Network{index: map[string]int{}}
}

// AddTask registers a task (interval variable) in the network.
func (n *Network) AddTask(name string) {
	if _, ok := n.index[name]; ok {
		return
	}
	i := len(n.names)
	n.names = append(n.names, name)
	n.index[name] = i
	for r := range n.c {
		n.c[r] = append(n.c[r], Universal)
	}
	n.c = append(n.c, make([]Relation, len(n.names)))
	for j := range n.c[i] {
		n.c[i][j] = Universal
	}
	n.c[i][i] = Equal
}

// Constrain intersects the current constraint between x and y with r
// (and the reverse constraint with Inverse(r)).
func (n *Network) Constrain(x, y string, r Relation) {
	i, j := n.index[x], n.index[y]
	n.c[i][j] &= r
	n.c[j][i] &= Inverse(r)
}

// Relation returns the (possibly tightened) constraint between x and y.
func (n *Network) Relation(x, y string) Relation {
	return n.c[n.index[x]][n.index[y]]
}

// PathConsistent enforces path consistency (PC-2): for every triple
// (i,k,j), C[i][j] is intersected with C[i][k] ∘ C[k][j], repeated until a
// fixpoint (the transitive closure of the network) is reached. It returns
// false if some constraint becomes empty, i.e. the network is inconsistent.
// The tightened constraints remain stored in the network.
func (n *Network) PathConsistent() bool {
	size := len(n.names)
	changed := true
	for changed {
		changed = false
		for k := 0; k < size; k++ {
			for i := 0; i < size; i++ {
				for j := 0; j < size; j++ {
					if i == j {
						continue
					}
					c := n.c[i][j] & Compose(n.c[i][k], n.c[k][j])
					if c == 0 {
						return false
					}
					if c != n.c[i][j] {
						n.c[i][j] = c
						changed = true
					}
				}
			}
		}
	}
	return true
}
