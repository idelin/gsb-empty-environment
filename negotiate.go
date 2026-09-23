// Package featureneg implements two-party capability negotiation.
//
// Both parties declare a protocol version range, required features and
// optional features. Features carry dependencies, mutual-exclusion edges
// and version conditions. Negotiation searches for the highest mutually
// satisfiable combination (max preference weight, then max versions),
// which a per-feature local intersection cannot find in general.
//
// The result is either a complete selection certificate bound to the
// session transcript, a minimal conflicting condition subset, or a
// separate "undecided" status when the search budget is exhausted.
package featureneg

import (
	"fmt"
	"sort"
)

// Range is an inclusive version range.
type Range struct {
	Min uint32
	Max uint32
}

// Intersect returns the common sub-range, or ok=false when empty.
func (r Range) Intersect(o Range) (Range, bool) {
	lo := r.Min
	if o.Min > lo {
		lo = o.Min
	}
	hi := r.Max
	if o.Max < hi {
		hi = o.Max
	}
	if lo > hi {
		return Range{}, false
	}
	return Range{Min: lo, Max: hi}, true
}

// Dep is a dependency on another feature with a version condition:
// the selected version of the target must be >= MinVersion.
type Dep struct {
	Name       string
	MinVersion uint32
}

// Feature describes one capability as supported by one party.
type Feature struct {
	Name      string
	Versions  Range
	Requires  []Dep    // dependencies with version conditions
	Conflicts []string // mutually exclusive feature names
}

// Offer is one party's negotiation declaration.
type Offer struct {
	Party      string
	Protocol   Range    // supported protocol version range
	Required   []string // features that must be selected
	Optional   []string // features that may be selected
	Preference []string // optional features, most preferred first
	Features   map[string]Feature
}

// Status is the negotiation outcome kind.
type Status int

const (
	// OK: a complete selection certificate was produced.
	OK Status = iota
	// Conflict: unsatisfiable; Reasons and ConflictCore explain why.
	Conflict
	// Undecided: the search budget was exhausted before the space was
	// fully explored. Reported separately from real conflicts.
	Undecided
)

func (s Status) String() string {
	switch s {
	case OK:
		return "ok"
	case Conflict:
		return "conflict"
	case Undecided:
		return "undecided"
	}
	return "unknown"
}

// Selection is one chosen feature at a concrete version.
type Selection struct {
	Name    string
	Version uint32
}

// Outcome is the result of one negotiation round.
type Outcome struct {
	Status          Status
	ProtocolVersion uint32
	Selected        []Selection // chosen features (best-so-far when Undecided)
	Reasons         []string    // rejection reasons
	ConflictCore    []string    // minimal conflicting condition subset
	Certificate     *Certificate
}

// DefaultBudget bounds solver search nodes when the caller passes <= 0.
const DefaultBudget = 1 << 20

// Negotiate runs one negotiation round for the session.
func (s *Session) Negotiate(budget int) Outcome {
	if budget <= 0 {
		budget = DefaultBudget
	}
	var out Outcome

	proto, ok := s.Local.Protocol.Intersect(s.Remote.Protocol)
	if !ok {
		out.Status = Conflict
		out.Reasons = append(out.Reasons, fmt.Sprintf(
			"protocol version ranges [%d,%d] and [%d,%d] have an empty intersection",
			s.Local.Protocol.Min, s.Local.Protocol.Max,
			s.Remote.Protocol.Min, s.Remote.Protocol.Max))
		return out
	}

	ctx := buildCtx(s.Local, s.Remote)
	required := unionSorted(s.Local.Required, s.Remote.Required)

	// A required feature unsupported by the peer (or with an empty
	// version intersection) must fail.
	var missing []string
	for _, name := range required {
		if !ctx.negotiable[name] {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		out.Status = Conflict
		for _, name := range missing {
			out.Reasons = append(out.Reasons, fmt.Sprintf(
				"required feature %q is not supported by both parties at a common version", name))
		}
		out.ConflictCore = missing // each is independently fatal: already minimal
		return out
	}

	sol := newSolver(ctx, required, budget)
	best, found := sol.run()
	switch {
	case sol.exhausted:
		out.Status = Undecided
		out.Reasons = append(out.Reasons, fmt.Sprintf(
			"search budget of %d nodes exhausted; result is provisional", budget))
		if found {
			out.ProtocolVersion = proto.Max
			out.Selected = ctx.selectionList(best)
		}
		return out
	case !found:
		out.Status = Conflict
		out.Reasons = append(out.Reasons,
			"the required feature set is internally infeasible (dependency, conflict or version condition)")
		out.ConflictCore = minimizeCore(ctx, required)
		return out
	}

	out.Status = OK
	out.ProtocolVersion = proto.Max
	out.Selected = ctx.selectionList(best)
	out.Certificate = &Certificate{
		Transcript:      s.Transcript(),
		ProtocolVersion: proto.Max,
		Selected:        out.Selected,
	}
	return out
}

// negCtx is the merged negotiation context of both offers.
type negCtx struct {
	features   map[string]Feature // merged constraints (union of both sides)
	inter      map[string]Range   // version intersection per negotiable feature
	negotiable map[string]bool
	optional   []string // optional candidate names, sorted
	weight     map[string]uint64
}

func buildCtx(local, remote *Offer) *negCtx {
	ctx := &negCtx{
		features:   map[string]Feature{},
		inter:      map[string]Range{},
		negotiable: map[string]bool{},
		weight:     map[string]uint64{},
	}
	for name, lf := range local.Features {
		rf, ok := remote.Features[name]
		if !ok {
			continue
		}
		inter, ok := lf.Versions.Intersect(rf.Versions)
		if !ok {
			continue
		}
		merged := Feature{
			Name:      name,
			Versions:  inter,
			Requires:  append(append([]Dep{}, lf.Requires...), rf.Requires...),
			Conflicts: unionSorted(lf.Conflicts, rf.Conflicts),
		}
		ctx.features[name] = merged
		ctx.inter[name] = inter
		ctx.negotiable[name] = true
	}
	// An optional feature is a candidate only if both parties offer it;
	// this is what lets a downgrade request narrow the negotiated set.
	remoteOpt := map[string]bool{}
	for _, n := range remote.Optional {
		remoteOpt[n] = true
	}
	optSet := map[string]bool{}
	for _, n := range local.Optional {
		if remoteOpt[n] {
			optSet[n] = true
		}
	}
	for name := range ctx.negotiable {
		if optSet[name] {
			ctx.optional = append(ctx.optional, name)
			ctx.weight[name] = prefWeight(local, remote, name)
		}
	}
	sort.Strings(ctx.optional)
	return ctx
}

func prefWeight(local, remote *Offer, name string) uint64 {
	w := uint64(1)
	for _, o := range []*Offer{local, remote} {
		for i, n := range o.Preference {
			if n == name {
				w += uint64(len(o.Preference)-i) * 16
			}
		}
	}
	return w
}

// close computes the dependency closure of sel, adding negotiable
// dependency targets implicitly, then validates conflicts and version
// conditions. It returns ok=false if the set is infeasible.
func (ctx *negCtx) close(sel selection) (selection, bool) {
	for {
		changed := false
		for name := range sel {
			for _, d := range ctx.features[name].Requires {
				if sel[d.Name] {
					continue
				}
				if !ctx.negotiable[d.Name] {
					return nil, false
				}
				sel[d.Name] = true
				changed = true
			}
		}
		if !changed {
			break
		}
	}
	for name := range sel {
		f := ctx.features[name]
		for _, c := range f.Conflicts {
			if sel[c] {
				return nil, false
			}
		}
		for _, d := range f.Requires {
			// Versions are chosen maximal, so a >= condition is
			// satisfiable iff the intersection maximum satisfies it.
			if ctx.inter[d.Name].Max < d.MinVersion {
				return nil, false
			}
		}
	}
	return sel, true
}

// scoreOf ranks a closed selection: preference weight first, then the
// sum of chosen (maximal) versions.
func (ctx *negCtx) scoreOf(sel selection) score {
	var sc score
	for name := range sel {
		sc.pref += ctx.weight[name]
		sc.vers += uint64(ctx.inter[name].Max)
	}
	return sc
}

func (ctx *negCtx) selectionList(sel selection) []Selection {
	names := make([]string, 0, len(sel))
	for name := range sel {
		names = append(names, name)
	}
	sort.Strings(names)
	out := make([]Selection, 0, len(names))
	for _, name := range names {
		out = append(out, Selection{Name: name, Version: ctx.inter[name].Max})
	}
	return out
}

// feasibleRequired reports whether the forced required set alone can be
// closed into a feasible selection. Used for minimal-core extraction.
func (ctx *negCtx) feasibleRequired(required []string) bool {
	sel := selection{}
	for _, name := range required {
		if !ctx.negotiable[name] {
			return false
		}
		sel[name] = true
	}
	_, ok := ctx.close(sel)
	return ok
}

func unionSorted(lists ...[]string) []string {
	set := map[string]bool{}
	for _, l := range lists {
		for _, s := range l {
			set[s] = true
		}
	}
	out := make([]string, 0, len(set))
	for s := range set {
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}
