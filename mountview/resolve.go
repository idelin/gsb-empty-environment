package mountview

import "fmt"

// resolved is the may-set of content groups reachable from one target handle.
type resolved struct {
	groups    *setG
	absent    bool
	uncertain bool
	reasons   []string
}

// Evaluate works like Resolve but additionally applies a policy ceiling. The
// verdict is Rejected only when every compatible history is over-privileged or
// the binding is deterministically absent; any residual indeterminacy stays
// Unknown so an in-gap relaxation can never be mistaken for safe inheritance.
func (e *Engine) Evaluate(target string, policy *Policy) (*TargetResult, error) {
	res, err := e.Resolve(target)
	if err != nil {
		return nil, err
	}
	if policy == nil || res.Verdict != Recoverable {
		return res, nil
	}
	key := PolicyKey{Object: res.Objects[0], Interval: res.Intervals[0]}
	if !policy.CheckPerm(key.Object, key.Interval, res.Perms[0]) {
		res.Verdict = Rejected
		res.Reason = "deterministic effective mask relaxes the policy ceiling"
	}
	return res, nil
}

// Resolve walks the mount view for target with symbolic-link resolution under
// may-semantics: any link whose target is not fully certain immediately makes
// the result uncertain, because an in-gap repoint must never be treated as a
// safe inherited alias.
func (e *Engine) Resolve(target string) (*TargetResult, error) {
	tp, err := CleanPath(target)
	if err != nil {
		return nil, err
	}
	r := &resolved{groups: newSetG()}
	e.resolvePath(tp, 0, r)

	res := &TargetResult{Path: tp}
	if r.uncertain {
		res.Verdict = Unknown
		res.Objects, res.Intervals, res.Perms = e.collectMay(r.groups)
		res.Reason = joinReasons(r.reasons)
		return res, nil
	}

	gid, ok := r.groups.singleton()
	switch {
	case !ok && (r.absent || len(r.groups.m) == 0):
		res.Verdict = Rejected
		res.Reason = "target handle is absent in every compatible history"
	case !ok:
		res.Verdict = Unknown
		res.Objects, res.Intervals, res.Perms = e.collectMay(r.groups)
		res.Reason = "target resolves to multiple distinct content groups across compatible histories"
	default:
		return e.evaluateGroup(tp, gid), nil
	}
	return res, nil
}

func (e *Engine) resolvePath(p string, hops int, r *resolved) {
	if hops > maxSymlinkHops {
		r.uncertain = true
		r.reasons = append(r.reasons, fmt.Sprintf("symlink resolution exceeds %d hops at %s", maxSymlinkHops, p))
		return
	}
	cell := e.paths[p]
	if cell == nil {
		r.absent = true
		return
	}
	if cell.mayAbsent {
		r.absent = true
		r.uncertain = true
		r.reasons = append(r.reasons, fmt.Sprintf("path %s may be removed inside a missing window", p))
	}
	if !cell.present && !cellMayOpen(cell) {
		r.absent = true
		return
	}

	// Symbolic-link branch.
	if cell.symlink || len(cell.mayTargets) > 0 {
		if len(cell.mayTargets) != 1 || cell.mayAbsent {
			r.uncertain = true
			r.reasons = append(r.reasons, fmt.Sprintf("symlink at %s has an open target set", p))
			return
		}
		var target string
		for t := range cell.mayTargets {
			target = t
		}
		e.resolvePath(target, hops+1, r)
		return
	}

	// Content-group branch (may include hardlink alias sets).
	r.groups.addAll(cell.mayGroups)
	if cell.mayGroups.any {
		r.uncertain = true
		r.reasons = append(r.reasons, fmt.Sprintf("path %s may bind a group minted inside a missing window", p))
	}
}

func (e *Engine) evaluateGroup(path string, gid GroupID) *TargetResult {
	g := e.groups[gid]
	res := &TargetResult{Path: path}
	objects := setO{}
	if g != nil && g.present {
		objects.add(g.exactObject)
	}
	for o := range g.mayObjects.m {
		objects.add(o)
	}
	intervals := setI{}
	if g != nil && g.present {
		intervals.add(g.exactInter)
	}
	for iv := range g.mayInters {
		intervals.add(iv)
	}
	perms := setP{}
	if g != nil && g.present {
		perms.add(g.exactPerm)
	}
	for pm := range g.mayPerms {
		perms.add(pm)
	}

	res.Objects = objects.list()
	res.Intervals = intervals.list()
	res.Perms = perms.list()

	open := (g != nil && g.mayObjects.any) || (g != nil && g.mayAbsent)
	if open || len(objects.m) != 1 || len(intervals) != 1 || len(perms) != 1 {
		res.Verdict = Unknown
		res.Reason = "object id, interval or protection bits are not fixed in every compatible history"
		return res
	}
	res.Verdict = Recoverable
	res.Reason = "single immutable object, interval and effective mask in every compatible history"
	return res
}

func (e *Engine) collectMay(groups *setG) ([]ObjectID, []Interval, []Perm) {
	objs := setO{}
	inters := setI{}
	perms := setP{}
	for gid := range groups.m {
		g := e.groups[gid]
		if g == nil {
			continue
		}
		if g.present {
			objs.add(g.exactObject)
			inters.add(g.exactInter)
			perms.add(g.exactPerm)
		}
		for o := range g.mayObjects.m {
			objs.add(o)
		}
		for iv := range g.mayInters {
			inters.add(iv)
		}
		for pm := range g.mayPerms {
			perms.add(pm)
		}
	}
	return objs.list(), inters.list(), perms.list()
}

func joinReasons(rs []string) string {
	out := ""
	for i, r := range rs {
		if i > 0 {
			out += "; "
		}
		out += r
	}
	return out
}
