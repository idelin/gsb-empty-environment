package causal

import (
	"fmt"
	"sort"
)

// Request asks for a slice. Target pins exact wanted versions per object; an
// object listed in Latest means "the newest locally known version". Target
// and Latest must be disjoint. A request is a value used by one Select call;
// the selector keeps no per-request state anywhere else.
type Request struct {
	ID     string
	Target VV       // exact pin, may be nil
	Latest []string // objects whose newest known version is wanted
}

// MissingCause explains why one wanted version is not part of the slice.
type MissingCause struct {
	Cause   Ref
	Present bool
}

// Block is one blocking dependency: to include Want, Cause must first arrive.
type Block struct {
	Want    Ref
	Cause   Ref
	Present bool
}

func (b Block) String() string {
	state := "missing"
	if b.Present {
		state = "present-but-itself-blocked"
	}
	return fmt.Sprintf("%s waits-for %s (%s)", b.Want, b.Cause, state)
}

// Selection is the answer to one request.
//
//	Selected: the returned slice restricted to requested objects.
//	Closure:  the same slice expanded to every dependency object.
//	Blocking: frontier versions that could not be included and why.
//	Complete: true iff no wanted frontier is blocked.
type Selection struct {
	Selected VV
	Closure  VV
	Blocking []Block
	Complete bool
}

// View is an immutable snapshot of what one replica currently holds.
//
//	Present: versions delivered and readable.
//	Known:   versions advertised as existing (gossip digests), bounding
//	         "newest" requests even before delivery.
//
// BuildView copies both sets so later deliveries cannot mutate a snapshot.
type View struct {
	Present map[Ref]bool
	Known   map[Ref]bool
}

func BuildView(present, known map[Ref]bool) View {
	cp := func(in map[Ref]bool) map[Ref]bool {
		out := make(map[Ref]bool, len(in))
		for r, ok := range in {
			out[r] = ok
		}
		return out
	}
	return View{Present: cp(present), Known: cp(known)}
}

// locallyFeasible reports whether ref is present and all of its causal deps
// are locally feasible (ignoring request caps). The dependency graph is a DAG
// (edges point to older versions), so the memoized recursion terminates.
func (v View) locallyFeasible(reg *Registry, ref Ref, visiting map[Ref]bool, memo map[Ref]bool) bool {
	if ok, cached := memo[ref]; cached {
		return ok
	}
	if visiting[ref] {
		return false
	}
	ok := false
	if ver, known := reg.Lookup(ref); known && v.Present[ref] {
		ok = true
		visiting[ref] = true
		for _, d := range ver.Deps {
			if !v.locallyFeasible(reg, d, visiting, memo) {
				ok = false
				break
			}
		}
		delete(visiting, ref)
	}
	memo[ref] = ok
	return ok
}

// oneMissing returns one missing upstream version that makes ref locally
// infeasible, preferring the deepest missing root.
func (v View) oneMissing(reg *Registry, ref Ref, visiting map[Ref]bool, memo map[Ref]MissingCause) (MissingCause, bool) {
	if res, cached := memo[ref]; cached {
		return res, res.Cause != (Ref{})
	}
	if visiting[ref] {
		return MissingCause{Cause: ref, Present: v.Present[ref]}, true
	}
	res := MissingCause{}
	found := false
	ver, known := reg.Lookup(ref)
	switch {
	case !known || !v.Present[ref]:
		res, found = MissingCause{Cause: ref, Present: v.Present[ref]}, true
	default:
		visiting[ref] = true
		for _, d := range ver.Deps {
			if c, bad := v.oneMissing(reg, d, visiting, memo); bad {
				res, found = c, true
				break
			}
		}
		delete(visiting, ref)
	}
	memo[ref] = res
	return res, found
}

// Select computes the greatest causally consistent slice admitted by the
// request. It is a pure function of (reg, view, req): the view is never
// mutated, and separate requests share no mutable snapshot state.
func Select(reg *Registry, view View, req Request) Selection {
	// caps[o] is the request's own upper bound per object:
	//   Latest -> newest version this replica knows to exist;
	//   Target -> the pinned value.
	caps := VV{}
	latestSet := map[string]bool{}
	for _, o := range req.Latest {
		if latestSet[o] {
			continue
		}
		latestSet[o] = true
		if _, pinned := req.Target[o]; pinned {
			panic(fmt.Sprintf("causal: object %q appears in both Target and Latest", o))
		}
		for seq := reg.MaxSeq(o); seq >= 1; seq-- {
			ref := Ref{Object: o, Seq: seq}
			if view.Known[ref] || view.Present[ref] {
				caps[o] = seq
				break
			}
		}
	}
	for o, n := range req.Target {
		if n >= 0 {
			caps[o] = n
		}
	}

	requested := map[string]bool{}
	for _, o := range req.Latest {
		requested[o] = true
	}
	for o := range req.Target {
		requested[o] = true
	}
	objs := sortedKeys(caps)

	feasMemo := map[Ref]bool{}
	missMemo := map[Ref]MissingCause{}

	// sliceFor(frontiers) is the dependency closure of the chosen prefixes.
	sliceFor := func(front map[string]int) VV {
		out := VV{}
		for o, n := range front {
			if n > 0 {
				out.merge(causalClosure(reg, Ref{Object: o, Seq: n}))
			}
		}
		return out
	}
	// chosen[o] is the frontier currently considered for object o; it only
	// ever decreases. Start at the cap and repeatedly lower any object whose
	// current frontier cannot belong to an admissible slice:
	//   - its closure needs a version that is absent (missing cause), or
	//   - its closure overruns another requested object's current frontier.
	// Lowering one object can only invalidate closures that needed the
	// removed versions, so the iteration is monotone and finite.
	chosen := map[string]int{}
	for _, o := range objs {
		chosen[o] = caps[o]
	}
	locallyOK := func(ref Ref) bool {
		return view.locallyFeasible(reg, ref, map[Ref]bool{}, feasMemo)
	}
	for {
		progress := false
		for _, o := range objs {
			for chosen[o] > 0 {
				ref := Ref{Object: o, Seq: chosen[o]}
				cl := causalClosure(reg, ref)
				ok := locallyOK(ref)
				if ok {
					for co, cn := range cl {
						// A closure component on a requested object must not
						// exceed that object's current frontier; on a
						// non-requested object it must be locally present.
						if requested[co] {
							cur, there := chosen[co]
							if there && cn > cur {
								ok = false
								break
							}
						} else if !view.locallyFeasible(reg, Ref{Object: co, Seq: cn},
							map[Ref]bool{}, feasMemo) {
							ok = false
							break
						}
					}
				}
				if ok {
					break
				}
				chosen[o]--
				progress = true
			}
		}
		if !progress {
			break
		}
	}

	closure := sliceFor(chosen)

	// One blocking entry per requested object whose frontier sits below its
	// cap: the first excluded version with one concrete root cause. Prefix
	// closure makes all later versions equivalent for that object.
	blocking := []Block{}
	for _, o := range objs {
		if chosen[o] >= caps[o] {
			continue
		}
		ref := Ref{Object: o, Seq: chosen[o] + 1}
		cause, bad := view.oneMissing(reg, ref, map[Ref]bool{}, missMemo)
		if !bad {
			// Locally deliverable but excluded by a cap on a cause: report
			// the requested cap version that the closure overruns.
			cause = capCause(reg, ref, closure, caps, requested)
		}
		blocking = append(blocking, Block{Want: ref, Cause: cause.Cause, Present: cause.Present})
	}
	sort.Slice(blocking, func(i, j int) bool {
		if blocking[i].Want.Object != blocking[j].Want.Object {
			return blocking[i].Want.Object < blocking[j].Want.Object
		}
		return blocking[i].Want.Seq < blocking[j].Want.Seq
	})

	selected := VV{}
	for _, o := range req.Latest {
		if n := closure[o]; n > 0 {
			selected[o] = n
		}
	}
	for o, n := range req.Target {
		if c := closure[o]; c > 0 && c <= n {
			selected[o] = c
		}
	}

	return Selection{
		Selected: selected,
		Closure:  closure,
		Blocking: blocking,
		Complete: len(blocking) == 0,
	}
}

// capCause builds a synthetic cause for a version that is locally present but
// excluded because its closure overruns another requested object's cap.
func capCause(reg *Registry, ref Ref, closure, caps VV, requested map[string]bool) MissingCause {
	cl := causalClosure(reg, ref)
	for o, n := range cl {
		if requested[o] {
			if bound, ok := caps[o]; ok && n > bound {
				return MissingCause{Cause: Ref{Object: o, Seq: bound + 1}, Present: false}
			}
		}
	}
	return MissingCause{Cause: ref, Present: true}
}

// causalClosure returns the minimal version vector containing ref and all of
// its transitive dependencies. Traversal must visit every reachable node: a
// higher seq already seen for an object does NOT make a lower node's deps
// redundant, so pruning is done per-node, not per-object-seq.
func causalClosure(reg *Registry, ref Ref) VV {
	out := VV{}
	visited := map[Ref]bool{}
	var walk func(r Ref)
	walk = func(r Ref) {
		if visited[r] {
			return
		}
		visited[r] = true
		if out[r.Object] < r.Seq {
			out[r.Object] = r.Seq
		}
		ver, ok := reg.Lookup(r)
		if !ok {
			return
		}
		for _, d := range ver.Deps {
			walk(d)
		}
	}
	walk(ref)
	return out
}

func sortedKeys(m VV) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
