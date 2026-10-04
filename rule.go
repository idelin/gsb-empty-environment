// Package fieldauth provides atomic, rule-based authorization for
// field-level record updates.
//
// A caller proposes a Patch (a set of field changes) against a committed
// Record. The engine applies the patch tentatively, recomputes derived
// fields, and evaluates every guarding rule. The update is authorized
// only if ALL triggered rules pass; otherwise the whole batch is
// rejected with a minimal Denial explanation. Nothing is applied
// partially.
//
// Conditions explicitly declare which image of the record they read:
// the pre-image (Old, committed values) or the post-image (New, values
// after the update would be committed). The actor's roles are always
// resolved from the pre-image, so a patch cannot grant itself new
// privileges within the same atomic update (e.g. "set my role to admin,
// then edit a sensitive field" is authorized as the OLD, non-admin
// identity and rejected).
package fieldauth

import "fmt"

// Record is a committed record: field name -> value.
type Record map[string]interface{}

// Patch is a proposed set of field changes.
type Patch map[string]interface{}

// Clone returns a shallow copy of the record.
func (r Record) Clone() Record {
	c := make(Record, len(r))
	for k, v := range r {
		c[k] = v
	}
	return c
}

// Source declares which image of the record a condition reads.
type Source int

const (
	// Old conditions read committed values (the pre-image).
	Old Source = iota
	// New conditions read the would-be committed values (the post-image).
	New
	// Both conditions compare the pre-image against the post-image.
	Both
)

func (s Source) String() string {
	switch s {
	case Old:
		return "pre-image"
	case New:
		return "post-image"
	case Both:
		return "transition"
	}
	return "unknown"
}

// Auth is the authorization context of the actor. Roles are resolved by
// the engine from the pre-image record BEFORE any proposed change is
// applied, so privileges can never be escalated by the update being
// authorized itself.
type Auth struct {
	ActorID string
	Roles   []string
}

// HasRole reports whether the actor holds the given role (as resolved
// from the pre-image).
func (a Auth) HasRole(role string) bool {
	for _, r := range a.Roles {
		if r == role {
			return true
		}
	}
	return false
}

// Cond is a single condition. Its Source declares which record image it
// reads; use the OldCond / NewCond / BothCond constructors so the
// declaration is enforced by construction.
type Cond struct {
	Desc string
	Src  Source
	ok   func(old, new Record, auth Auth) bool
}

// OldCond builds a condition that reads only the pre-image.
func OldCond(desc string, f func(old Record, auth Auth) bool) Cond {
	return Cond{Desc: desc, Src: Old, ok: func(old, _ Record, a Auth) bool {
		return f(old, a)
	}}
}

// NewCond builds a condition that reads only the post-image.
func NewCond(desc string, f func(new Record, auth Auth) bool) Cond {
	return Cond{Desc: desc, Src: New, ok: func(_, new Record, a Auth) bool {
		return f(new, a)
	}}
}

// BothCond builds a condition that compares the pre- and post-image.
func BothCond(desc string, f func(old, new Record, auth Auth) bool) Cond {
	return Cond{Desc: desc, Src: Both, ok: f}
}

// ActorHasRole is the canonical authorization condition: the actor must
// hold the role according to the pre-image. Because roles are resolved
// before the patch is applied, a patch that sets role=admin does NOT
// satisfy this condition within the same update.
func ActorHasRole(role string) Cond {
	return OldCond(fmt.Sprintf("actor must have role %q", role),
		func(_ Record, a Auth) bool { return a.HasRole(role) })
}

func (c Cond) eval(old, new Record, auth Auth) bool {
	return c.ok(old, new, auth)
}

// Rule guards a set of fields. It is triggered when the atomic update
// would change any guarded field (including derived fields that change
// as a consequence of the patch). When triggered, every condition must
// hold. A rule with no Fields guards every field.
type Rule struct {
	ID     string
	Fields []string
	Conds  []Cond
}

// guards reports whether the rule is triggered by the changed fields.
func (r Rule) triggeredBy(changed map[string]bool) []string {
	var trig []string
	if len(r.Fields) == 0 {
		for f := range changed {
			trig = append(trig, f)
		}
		return trig
	}
	for _, f := range r.Fields {
		if changed[f] {
			trig = append(trig, f)
		}
	}
	return trig
}
