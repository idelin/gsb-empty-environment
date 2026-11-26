package joinenum

import (
	"errors"
	"fmt"
	"math/bits"
)

// Plan is a physical join tree.  Leaves have Kind=InnerJoin, Leaf>=0 and nil
// children; internal nodes have Kind, Pred and two children.
type Plan struct {
	Kind        OpKind
	Leaf        int
	Pred        *Predicate
	Left        *Plan
	Right       *Plan
	Relations   RelationSet
	Schema      RelationSet
	Cost        int64
	Cardinality int64
	Encoding    string
}

// IsLeaf reports whether p is a base-relation leaf.
func (p *Plan) IsLeaf() bool { return p != nil && p.Left == nil && p.Right == nil && p.Leaf >= 0 }

// String returns the canonical pre-order encoding of p.
func (p *Plan) String() string {
	if p == nil {
		return ""
	}
	return p.Encoding
}

// Result is the outcome of enumeration.
type Result struct {
	Plan        *Plan
	Cost        int64
	Cardinality int64
	Encoding    string
}

// ErrNoPlan reports that no plan satisfied the TES and conflict constraints.
var ErrNoPlan = errors.New("joinenum: no legal plan")

// Optimize validates in, builds the optimizer metadata and returns the
// cheapest conflict-aware bushy plan.
func Optimize(in Input) (*Result, error) {
	q, err := NewQueryWithCost(in.Relations, in.Root, in.Cost)
	if err != nil {
		return nil, err
	}
	return q.Optimize()
}

// Build validates in and returns the optimizer description without running
// enumeration.
func Build(in Input) (*Query, error) {
	return NewQueryWithCost(in.Relations, in.Root, in.Cost)
}

const maxDPRelations = 12

type groupKey struct {
	leaves RelationSet
	ops    uint64
}

type planGroups map[groupKey]map[RelationSet][]*Plan

// Optimize enumerates the conflict-aware core search space and returns the
// cheapest semantically valid bushy plan.  Ties are broken by the
// lexicographically smallest canonical pre-order encoding.
func (q *Query) Optimize() (*Result, error) {
	plans, err := q.enumerate(false)
	if err != nil {
		return nil, err
	}
	return q.pick(plans)
}

// LegalPlans returns the final Pareto-optimal legal plans for diagnostics.
// Plans dominated in both cost and cardinality are omitted.
func (q *Query) LegalPlans() ([]*Result, error) {
	plans, err := q.enumerate(false)
	if err != nil {
		return nil, err
	}
	out := make([]*Result, len(plans))
	for i, p := range plans {
		out[i] = &Result{Plan: p, Cost: p.Cost, Cardinality: p.Cardinality, Encoding: p.Encoding}
	}
	return out, nil
}

// EnumerateUnrestricted enumerates the same operator/predicate multiset as if
// every operator were an inner join, ignoring TES and conflict rules.  It is
// intended as a lower-bound comparison for tests and diagnostics.
func (q *Query) EnumerateUnrestricted() (*Result, error) {
	plans, err := q.enumerate(true)
	if err != nil {
		return nil, err
	}
	return q.pick(plans)
}

func (q *Query) pick(plans []*Plan) (*Result, error) {
	if len(plans) == 0 {
		return nil, ErrNoPlan
	}
	best := plans[0]
	for _, p := range plans[1:] {
		if p.Cost < best.Cost || (p.Cost == best.Cost && p.Encoding < best.Encoding) {
			best = p
		}
	}
	return &Result{Plan: best, Cost: best.Cost, Cardinality: best.Cardinality, Encoding: best.Encoding}, nil
}

func (q *Query) enumerate(unrestricted bool) ([]*Plan, error) {
	n := len(q.relations)
	if n > maxDPRelations {
		return nil, fmt.Errorf("%w: %d relations exceed the DP limit of %d", ErrInvalidInput, n, maxDPRelations)
	}
	states := make(planGroups)
	allRels := q.allRels
	for size := 1; size <= n; size++ {
		for s := RelationSet(0); s <= allRels; s++ {
			if s.Len() != size {
				continue
			}
			if !unrestricted && !q.isConnected(s) {
				continue
			}
			if size == 1 {
				r := s.Elements()[0]
				leaf := &Plan{
					Kind:        InnerJoin,
					Leaf:        r,
					Relations:   s,
					Schema:      s,
					Cost:        0,
					Cardinality: q.relations[r].Cardinality,
					Encoding:    q.names[r],
				}
				states[groupKey{leaves: s, ops: 0}] = map[RelationSet][]*Plan{s: {leaf}}
				continue
			}
			masks := q.masksOfSize(size - 1)
			for _, mask := range masks {
				groups := make(map[RelationSet][]*Plan)
				overflow := false
				for _, oid := range maskIDs(mask) {
					op := q.byID[oid]
					rest := mask &^ (uint64(1) << uint(oid))
					s.ProperSubsets(func(s1 RelationSet) {
						s2 := s.Difference(s1)
						k1, k2 := s1.Len(), s2.Len()
						if k1 == 0 || k2 == 0 || k1-1 > bits.OnesCount64(rest) {
							return
						}
						RelationSet(rest).SubsetIter(func(m1 uint64) {
							if bits.OnesCount64(m1) != k1-1 {
								return
							}
							m2 := rest &^ m1
							if bits.OnesCount64(m2) != k2-1 {
								return
							}
							leftStates := states[groupKey{leaves: s1, ops: m1}]
							rightStates := states[groupKey{leaves: s2, ops: m2}]
							if len(leftStates) == 0 || len(rightStates) == 0 {
								return
							}
							for schema1, leftPlans := range leftStates {
								for schema2, rightPlans := range rightStates {
									outSchema, ok := q.validateSplit(op, m1, m2, schema1, schema2, unrestricted)
									if !ok {
										continue
									}
									for _, lp := range leftPlans {
										for _, rp := range rightPlans {
											kind := op.Kind
											sel := op.Pred.Selectivity
											if unrestricted {
												kind = InnerJoin
											}
											outCard, localCost, err := q.model.Join(kind, lp.Cardinality, rp.Cardinality, sel)
											if err != nil {
												overflow = true
												continue
											}
											cost, ok := checkedAdd(lp.Cost, rp.Cost)
											if !ok {
												overflow = true
												continue
											}
											cost, ok = checkedAdd(cost, localCost)
											if !ok {
												overflow = true
												continue
											}
											p := &Plan{
												Kind:        kind,
												Leaf:        -1,
												Pred:        op.Pred,
												Left:        lp,
												Right:       rp,
												Relations:   s,
												Schema:      outSchema,
												Cost:        cost,
												Cardinality: outCard,
												Encoding:    string(kind.Token()) + "(" + lp.Encoding + "," + rp.Encoding + ")",
											}
											groups[outSchema] = insertPareto(groups[outSchema], p)
										}
									}
								}
							}
						})
					})
				}
				if overflow {
					return nil, ErrCardinalityOverflow
				}
				if len(groups) > 0 {
					states[groupKey{leaves: s, ops: mask}] = groups
				}
			}
		}
	}
	finalOps := q.allOps
	final := states[groupKey{leaves: allRels, ops: finalOps}]
	if len(final) == 0 {
		return nil, ErrNoPlan
	}
	if unrestricted {
		return final[allRels], nil
	}
	return final[q.originalSchema], nil
}

// SubsetIter calls fn for every submask of m, including 0 and m.
func (m RelationSet) SubsetIter(fn func(uint64)) {
	v := uint64(m)
	for sub := v; ; sub = (sub - 1) & v {
		fn(sub)
		if sub == 0 {
			return
		}
	}
}

func (q *Query) masksOfSize(size int) []uint64 {
	var out []uint64
	for m := uint64(0); m <= q.allOps; m++ {
		if bits.OnesCount64(m) == size {
			out = append(out, m)
		}
	}
	return out
}

func maskIDs(mask uint64) []int {
	out := make([]int, 0, bits.OnesCount64(mask))
	for v := mask; v != 0; v &= v - 1 {
		out = append(out, bits.TrailingZeros64(v))
	}
	return out
}

func checkedAdd(a, b int64) (int64, bool) {
	if b > 0 && a > (1<<63-1)-b {
		return 0, false
	}
	if b < 0 && a < (-1<<63)-b {
		return 0, false
	}
	return a + b, true
}

func (q *Query) validateSplit(op *Operator, leftOps, rightOps uint64, leftSchema, rightSchema RelationSet, unrestricted bool) (RelationSet, bool) {
	refs := op.Pred.refSet()
	if !refs.Subset(leftSchema.Union(rightSchema)) {
		return 0, false
	}
	if refs.Intersect(leftSchema).IsEmpty() || refs.Intersect(rightSchema).IsEmpty() {
		if !unrestricted {
			return 0, false
		}
	}
	if !unrestricted {
		// The applicability check is exactly TES(o) ⊆ S1 ∪ S2.  The
		// side-specific checks below additionally pin the protected right
		// input to the correct child.
		if !op.TES.Subset(leftSchema.Union(rightSchema)) {
			return 0, false
		}
		if op.Kind != InnerJoin {
			refs := op.Pred.refSet()
			rightRefs := refs.Intersect(op.RightRels)
			leftRefs := refs.Intersect(op.LeftRels)
			// The protected (right) input must expose every right-hand
			// predicate relation, and must not absorb a left-hand predicate
			// relation.  Output relations that are not referenced by the
			// predicate may legally be supplied by an ancestor (R-ASSCOM).
			if !rightRefs.Subset(rightSchema) {
				return 0, false
			}
			if !leftRefs.Intersect(rightSchema).IsEmpty() {
				return 0, false
			}
		}
		// Every original ancestor that has been pulled below op must satisfy
		// the applicable conflict rule.
		cur := op
		for cur.Parent >= 0 {
			b := q.byID[cur.Parent]
			side := cur.Side
			bit := uint64(1) << uint(b.ID)
			key := [2]int{op.ID, b.ID}
			if leftOps&bit != 0 {
				if side != RootSide && q.leftForbidden[key] {
					return 0, false
				}
			}
			if rightOps&bit != 0 {
				if side != RootSide && q.rightForbidden[key] {
					return 0, false
				}
			}
			cur = b
		}
	}
	if unrestricted || op.Kind == InnerJoin || op.Kind == LeftOuterJoin {
		return leftSchema.Union(rightSchema), true
	}
	return leftSchema, true
}

func insertPareto(list []*Plan, p *Plan) []*Plan {
	for _, e := range list {
		if dominates(e, p) {
			return list
		}
	}
	out := list[:0]
	for _, e := range list {
		if dominates(p, e) {
			continue
		}
		out = append(out, e)
	}
	return append(out, p)
}

func dominates(a, b *Plan) bool {
	if a.Cost < b.Cost && a.Cardinality <= b.Cardinality {
		return true
	}
	if a.Cost <= b.Cost && a.Cardinality < b.Cardinality {
		return true
	}
	return a.Cost == b.Cost && a.Cardinality == b.Cardinality && a.Encoding <= b.Encoding
}
