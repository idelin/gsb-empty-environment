package admission

import (
	"math/big"
	"sort"
)

// Policy is one deterministic admission rule: the action chosen at each
// public decision variable. Host-fixed failure-domain variables are included
// with their mandated action.
type Policy map[string]string

// flow is probability mass parked on one DAG node. Mass is split into the
// failure bucket and successful audit-category buckets at entry time, using
// the destination node's terminal kind; non-terminal mass stays in total only.
type flow struct {
	total      *big.Rat
	failure    *big.Rat
	categories map[string]*big.Rat
}

func newFlow(categories []string) *flow {
	f := &flow{
		total:      new(big.Rat),
		failure:    new(big.Rat),
		categories: make(map[string]*big.Rat, len(categories)),
	}
	for _, cat := range categories {
		f.categories[cat] = new(big.Rat)
	}
	return f
}

// Evaluate computes the exact terminal distribution under one policy.
func (a *Analyzer) Evaluate(p Policy) (*Distribution, error) {
	if err := a.checkPolicy(p); err != nil {
		return nil, err
	}
	categories := a.categories()
	mass := make([]*flow, len(a.order))
	for i := range mass {
		mass[i] = newFlow(categories)
	}
	a.addMass(mass[0], big.NewRat(1, 1), a.order[0])

	for i, id := range a.order {
		n := a.nodes[id]
		d := mass[i]
		switch n.Kind {
		case KindEntry:
			e := n.Out[0]
			a.addMass(mass[a.idx[e.To]], d.total, e.To)
		case KindDraw:
			for _, e := range n.Out {
				// Each branch gets its own value: sharing the *big.Rat would
				// let later additions on one branch mutate the other.
				w := new(big.Rat).Mul(new(big.Rat).Set(d.total), n.Draw.Table[e.OnOutcome])
				a.addMass(mass[a.idx[e.To]], w, e.To)
			}
		case KindDecision:
			action := p[a.vars[a.nodeVar[id]].id]
			e := a.actionEdge(n, action)
			a.addMass(mass[a.idx[e.To]], d.total, e.To)
		case KindTerminal:
			// Mass stays on this terminal.
		}
	}
	return a.collect(mass, categories), nil
}

func (a *Analyzer) checkPolicy(p Policy) error {
	for _, v := range a.vars {
		action, ok := p[v.id]
		if !ok {
			return &PolicyError{Variable: v.id, Reason: "missing action"}
		}
		if !containsString(v.actions, action) {
			return &PolicyError{Variable: v.id, Reason: "action not admissible: " + action}
		}
		if v.hostFixed && action != v.hostAct {
			return &PolicyError{Variable: v.id, Reason: "failure-domain variable is host-fixed to " + v.hostAct}
		}
	}
	return nil
}

// PolicyError reports an inadmissible policy.
type PolicyError struct {
	Variable string
	Reason   string
}

func (e *PolicyError) Error() string {
	return "admission: invalid policy at " + e.Variable + ": " + e.Reason
}

func (a *Analyzer) actionEdge(n *Node, action string) Edge {
	for _, e := range n.Out {
		if e.OnAction == action {
			return e
		}
	}
	// Compile already guarantees one edge per action.
	return n.Out[0]
}

// addMass attributes weight w arriving at node id according to its kind.
func (a *Analyzer) addMass(f *flow, w *big.Rat, id string) {
	w = deepCopyRat(w)
	f.total = addRat(f.total, w)
	n := a.nodes[id]
	if n.Kind != KindTerminal {
		return
	}
	if n.Terminal.Category == FailureCategory {
		f.failure = addRat(f.failure, w)
		return
	}
	bucket := f.categories[n.Terminal.Category]
	f.categories[n.Terminal.Category] = addRat(bucket, w)
}

func deepCopyRat(r *big.Rat) *big.Rat {
	out, _ := new(big.Rat).SetString(r.RatString())
	return out
}

// Distribution is the exact outcome of one deterministic admission rule.
type Distribution struct {
	// SuccessMass is the unconditional probability of each audit category.
	SuccessMass map[string]*big.Rat
	// FailureMass is the probability that the round cannot complete.
	FailureMass *big.Rat
	// ConditionalShare is the category allocation normalized over completed
	// rounds. It is nil when FailureMass is one.
	ConditionalShare map[string]*big.Rat
	// CompletionMass is the total probability of a completed round.
	CompletionMass *big.Rat
}

func (a *Analyzer) categories() []string {
	out := make([]string, 0, len(a.model.Ideal))
	for cat := range a.model.Ideal {
		out = append(out, cat)
	}
	sort.Strings(out)
	return out
}

func (a *Analyzer) collect(mass []*flow, categories []string) *Distribution {
	final := newFlow(categories)
	for i, id := range a.order {
		n := a.nodes[id]
		if n.Kind != KindTerminal {
			continue
		}
		final.failure = addRat(final.failure, mass[i].failure)
		for cat := range mass[i].categories {
			bucket := final.categories[cat]
			final.categories[cat] = addRat(bucket, mass[i].categories[cat])
		}
	}
	completion := new(big.Rat)
	success := map[string]*big.Rat{}
	for _, cat := range categories {
		p := new(big.Rat).Set(final.categories[cat])
		success[cat] = p
		completion.Add(completion, p)
	}
	out := &Distribution{
		SuccessMass:    success,
		FailureMass:    new(big.Rat).Set(final.failure),
		CompletionMass: completion,
	}
	if completion.Sign() > 0 {
		cond := map[string]*big.Rat{}
		for _, cat := range categories {
			cond[cat] = new(big.Rat).Quo(new(big.Rat).Set(success[cat]), new(big.Rat).Set(completion))
		}
		out.ConditionalShare = cond
	}
	return out
}

// addRat returns x+y in a fresh value, never mutating either operand. A plain
// new(big.Rat).Set(x) still shares the internal *big.Int buffers, so the
// operands are reconstructed from their canonical rational string.
func addRat(x, y *big.Rat) *big.Rat {
	return new(big.Rat).Add(deepCopyRat(x), deepCopyRat(y))
}
