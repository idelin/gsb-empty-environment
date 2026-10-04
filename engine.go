package fieldauth

import (
	"fmt"
	"reflect"
	"sort"
)

// RoleResolver resolves the actor's roles. The engine always invokes it
// with the pre-image (committed) record, before any proposed change is
// applied.
type RoleResolver func(actorID string, old Record) []string

// Engine authorizes atomic multi-field updates against a rule set.
type Engine struct {
	resolve   RoleResolver
	rules     []Rule
	derived   []Derived
	order     []int
	isDerived map[string]bool
}

// NewEngine creates an engine. resolve must not be nil.
func NewEngine(resolve RoleResolver) *Engine {
	return &Engine{resolve: resolve, isDerived: map[string]bool{}}
}

// Guard registers rules and returns the engine for chaining.
func (e *Engine) Guard(rules ...Rule) *Engine {
	e.rules = append(e.rules, rules...)
	return e
}

// Derive registers derived fields. They are recomputed, in dependency
// order, on every authorization and their recomputed values participate
// in rule checks. Returns an error on duplicates or dependency cycles.
func (e *Engine) Derive(ds ...Derived) error {
	e.derived = append(e.derived, ds...)
	order, err := topoOrder(e.derived)
	if err != nil {
		e.derived = e.derived[:len(e.derived)-len(ds)]
		return err
	}
	e.order = order
	for _, d := range ds {
		e.isDerived[d.Name] = true
	}
	return nil
}

// Authorize atomically checks a proposed multi-field update.
//
// On success it returns the post-image record (patch applied, derived
// fields recomputed). On rejection it returns a Denial with the minimal
// explanation; no partial result is ever produced. An error is returned
// only for malformed input (e.g. patching a derived field directly).
func (e *Engine) Authorize(actorID string, old Record, patch Patch) (Record, *Denial, error) {
	for f := range patch {
		if e.isDerived[f] {
			return nil, nil, fmt.Errorf("fieldauth: field %q is derived and cannot be patched directly", f)
		}
	}

	// Tentative post-image: apply patch, then recompute derived fields.
	newRec := old.Clone()
	for f, v := range patch {
		newRec[f] = v
	}
	for _, idx := range e.order {
		d := e.derived[idx]
		newRec[d.Name] = d.Compute(newRec)
	}

	changed := changedFields(old, newRec)

	// Resolve the actor's identity from the PRE-IMAGE. This is the
	// security-critical step: privileges in effect for this update are
	// those the actor held before it, never those the update grants.
	auth := Auth{ActorID: actorID, Roles: e.resolve(actorID, old)}

	denial := &Denial{triggered: map[string][]string{}}
	for _, r := range e.rules {
		trig := r.triggeredBy(changed)
		if len(trig) == 0 {
			continue
		}
		failed := false
		for _, c := range r.Conds {
			if !c.eval(old, newRec, auth) {
				denial.Failures = append(denial.Failures, Failure{Rule: r.ID, Cond: c.Desc, Src: c.Src})
				failed = true
			}
		}
		if failed {
			denial.triggered[r.ID] = trig
		}
	}
	if len(denial.Failures) > 0 {
		denial.finalize(e, patch)
		return nil, denial, nil
	}
	return newRec, nil, nil
}

// changedFields returns the set of fields whose value differs between
// the pre- and post-image, including derived fields.
func changedFields(old, new Record) map[string]bool {
	changed := map[string]bool{}
	for k, nv := range new {
		if !reflect.DeepEqual(old[k], nv) {
			changed[k] = true
		}
	}
	for k := range old {
		if _, ok := new[k]; !ok {
			changed[k] = true
		}
	}
	return changed
}

// baseCauses maps a changed field to the patch fields responsible for
// the change. Base fields map to themselves; derived fields map,
// transitively, to the patched fields they depend on.
func (e *Engine) baseCauses(field string, patch Patch, out map[string]bool) {
	if _, ok := patch[field]; ok {
		out[field] = true
		return
	}
	if e.isDerived[field] {
		for _, d := range e.derived {
			if d.Name != field {
				continue
			}
			for _, dep := range d.Depends {
				e.baseCauses(dep, patch, out)
			}
		}
		return
	}
	out[field] = true // not in patch and not derived: report as-is
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
