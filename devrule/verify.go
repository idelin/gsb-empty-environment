package devrule

import "fmt"

// TargetEntry is one symbolic entry of a target policy: a rectangle of
// devices (type selector plus major/minor number or Wildcard) and the
// permissions the policy allows or forbids there.
type TargetEntry struct {
	Type  TypeSel
	Major int
	Minor int
	Perms Perm
}

func (e TargetEntry) rects() []Region {
	return Rule{Type: e.Type, Major: e.Major, Minor: e.Minor, Perms: e.Perms}.rects()
}

func (e TargetEntry) String() string {
	return fmt.Sprintf("%s %s:%s %s", e.Type,
		numOrWildcard(e.Major), numOrWildcard(e.Minor), e.Perms)
}

// Target is the reference policy: a finite symbolic set of allowed devices
// plus a mandatory deny set that no compiled permission may touch.
type Target struct {
	Allowed []TargetEntry
	Denied  []TargetEntry
}

// allowedRectsFor returns the allowed rectangles of type k granting p.
func (t Target) allowedRectsFor(k Kind, p Perm) []Region {
	var out []Region
	for _, e := range t.Allowed {
		if e.Perms&p == 0 {
			continue
		}
		for _, r := range e.rects() {
			if r.Kind == k {
				out = append(out, r)
			}
		}
	}
	return out
}

// allowedPermsAt returns the permissions the target allows for one device.
func (t Target) allowedPermsAt(k Kind, major, minor int) Perm {
	var p Perm
	for _, e := range t.Allowed {
		for _, r := range e.rects() {
			if r.Kind == k && r.contains(major, minor) {
				p |= e.Perms
			}
		}
	}
	return p
}

// ViolationReason explains why a compiled permission is unsound.
type ViolationReason int

const (
	// NotInTargetAllow: the compiled permission is outside the allowed set.
	NotInTargetAllow ViolationReason = iota
	// HitsMandatoryDeny: the compiled permission touches the deny set.
	HitsMandatoryDeny
)

func (r ViolationReason) String() string {
	if r == NotInTargetAllow {
		return "not-in-target-allow"
	}
	return "hits-mandatory-deny"
}

// Violation is a soundness counterexample: the smallest device (type c<b,
// then major, minor, permission bit) whose compiled permissions break the
// target policy.
type Violation struct {
	Kind   Kind
	Major  int
	Minor  int
	Perms  Perm
	Reason ViolationReason
}

func (v Violation) String() string {
	return fmt.Sprintf("%s %d:%d %s (%s)", v.Kind, v.Major, v.Minor, v.Perms, v.Reason)
}

// Hint is a completeness counterexample: the smallest device that the target
// allows but the compiled diagram wrongly denies.
type Hint struct {
	Kind  Kind
	Major int
	Minor int
	Perm  Perm
}

func (h Hint) String() string {
	return fmt.Sprintf("%s %d:%d %s", h.Kind, h.Major, h.Minor, h.Perm)
}

// VerifyResult summarizes a policy check.
type VerifyResult struct {
	// Sound is true when every compiled permission stays inside the target
	// allowed set and clear of the mandatory deny set.
	Sound bool
	// Violation is the smallest unsound device, nil when Sound.
	Violation *Violation
	// Completeness is the smallest target-allowed device the diagram
	// denies, nil when the diagram covers the whole allowed set.
	Completeness *Hint
}

// permRank orders permission bits r < w < m by their lowest set bit.
func permRank(p Perm) int {
	for i, b := range permBits {
		if p&b != 0 {
			return i
		}
	}
	return len(permBits)
}

// deviceLess orders devices by type (c<b), major, minor, permission bit.
func deviceLess(k1 Kind, maj1, min1 int, p1 Perm, k2 Kind, maj2, min2 int, p2 Perm) bool {
	if k1 != k2 {
		return k1 < k2
	}
	if maj1 != maj2 {
		return maj1 < maj2
	}
	if min1 != min2 {
		return min1 < min2
	}
	return permRank(p1) < permRank(p2)
}

// Verify checks a compiled diagram against the target policy symbolically,
// without enumerating devices.
func Verify(d *Diagram, t Target) VerifyResult {
	res := VerifyResult{Sound: true}
	var best *Violation
	consider := func(v Violation) {
		if best == nil || deviceLess(v.Kind, v.Major, v.Minor, v.Perms,
			best.Kind, best.Major, best.Minor, best.Perms) {
			v := v
			best = &v
		}
	}
	for _, reg := range d.Regions {
		if reg.Perms == 0 {
			continue
		}
		// Every granted permission must be covered by the allowed set.
		for _, p := range permBits {
			if reg.Perms&p == 0 {
				continue
			}
			rem := subtractFromAll([]Region{reg}, t.allowedRectsFor(reg.Kind, p)...)
			if len(rem) == 0 {
				continue
			}
			maj, min := minDevice(rem)
			consider(Violation{
				Kind:   reg.Kind,
				Major:  maj,
				Minor:  min,
				Perms:  reg.Perms &^ t.allowedPermsAt(reg.Kind, maj, min),
				Reason: NotInTargetAllow,
			})
		}
		// No granted permission may touch the mandatory deny set.
		for _, e := range t.Denied {
			bad := reg.Perms & e.Perms
			if bad == 0 {
				continue
			}
			for _, er := range e.rects() {
				ov, ok := overlap(reg, er)
				if !ok {
					continue
				}
				consider(Violation{
					Kind:   reg.Kind,
					Major:  ov.MajLo,
					Minor:  ov.MinLo,
					Perms:  bad,
					Reason: HitsMandatoryDeny,
				})
			}
		}
	}
	if best != nil {
		res.Sound = false
		res.Violation = best
	}
	res.Completeness = completenessHint(d, t)
	return res
}

// completenessHint finds the smallest target-allowed device that the diagram
// fails to grant, ordered by type (c<b), major, minor, permission bit.
func completenessHint(d *Diagram, t Target) *Hint {
	for _, k := range []Kind{Char, Block} {
		var best *Hint
		for _, p := range permBits {
			rem := subtractFromAll(t.allowedRectsFor(k, p), d.regionsWithPerm(k, p)...)
			if len(rem) == 0 {
				continue
			}
			maj, min := minDevice(rem)
			if best == nil || maj < best.Major || (maj == best.Major && min < best.Minor) {
				best = &Hint{Kind: k, Major: maj, Minor: min, Perm: p}
			}
		}
		if best != nil {
			return best
		}
	}
	return nil
}

// SemDiff is the first semantic difference between two diagrams: the
// smallest device whose permission set changed.
type SemDiff struct {
	Kind   Kind
	Major  int
	Minor  int
	Before Perm
	After  Perm
}

func (s SemDiff) String() string {
	return fmt.Sprintf("%s %d:%d before=%s after=%s", s.Kind, s.Major, s.Minor, s.Before, s.After)
}

// Compare returns the smallest device (type c<b, major, minor, permission
// bit) where two diagrams grant different permissions, or nil when they are
// semantically equal.
func Compare(before, after *Diagram) *SemDiff {
	for _, k := range []Kind{Char, Block} {
		var best *SemDiff
		for _, p := range permBits {
			for _, pair := range [][2]*Diagram{{before, after}, {after, before}} {
				rem := subtractFromAll(pair[0].regionsWithPerm(k, p), pair[1].regionsWithPerm(k, p)...)
				if len(rem) == 0 {
					continue
				}
				maj, min := minDevice(rem)
				if best == nil || maj < best.Major || (maj == best.Major && min < best.Minor) {
					best = &SemDiff{Kind: k, Major: maj, Minor: min}
				}
			}
		}
		if best != nil {
			best.Before = before.Lookup(k, best.Major, best.Minor)
			best.After = after.Lookup(k, best.Major, best.Minor)
			return best
		}
	}
	return nil
}
