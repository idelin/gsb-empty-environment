// Package typeset computes the precise set of virtual-call targets that a
// binary protection service may allow.
//
// Method resolution follows the class-hierarchy-analysis model:
//
//   - A virtual call is resolved from the possible dynamic (most-derived)
//     types of the receiver object, not from the method name alone. Two
//     unrelated classes that happen to define a method with the same name are
//     never conflated.
//   - Virtual inheritance shares a single base subobject and therefore a
//     single override slot. If that slot ends up with final overriders from
//     classes that are not ancestors of one another, the slot is reported as
//     ambiguous instead of silently picking one target.
//   - Repeated non-virtual bases are distinct subobjects distinguished by
//     their inheritance path; their slots are independent.
//   - Inheritance cycles are rejected.
//
// Every resolved target carries the allocation point that the object came
// from and the subobject path that led to the called slot. Package soundness
// (the computed candidate set never misses a call that can actually happen)
// is cross-checked against a finite-object execution model; see
// CheckSoundness.
//
// Method identity is the method name. The model deliberately keeps a single
// signature per method, which is sufficient for a name-based protection
// allowlist; overloaded names can be pre-encoded into distinct names by the
// analysis host.
package typeset

// Class describes one class in the inheritance graph.
type Class struct {
	// Name is the unique class identifier.
	Name string

	// DirectBases lists the immediate base classes.
	DirectBases []Base

	// Methods are the methods declared directly on this class.
	Methods []Method
}

// Base is one direct inheritance edge.
type Base struct {
	// Name is the base class name.
	Name string

	// Virtual marks a virtual-inheritance edge. Virtual bases share a single
	// subobject (and a single override slot) inside a most-derived object.
	Virtual bool
}

// Method is a member function declaration.
type Method struct {
	// Name identifies the method. Two methods are the same virtual function
	// (and hence may override one another) when their names match.
	Name string

	// Virtual marks a virtual function. A virtual call may only be resolved
	// to virtual methods; the call is ill-formed otherwise.
	Virtual bool
}

// Origin records where a pointer value (or a resolved target) came from.
// It lets the protection service attribute every allowed target to the
// concrete allocation point and inheritance path that justified it.
type Origin struct {
	// AllocPoint is the allocation site that created the object.
	AllocPoint string

	// MostDerived is the most-derived class of the allocated object.
	MostDerived string

	// Path is the subobject path from MostDerived to the receiver subobject
	// involved in the call, outermost class first. Every PathStep is one
	// inheritance edge.
	Path []PathStep
}

// PathStep is one edge of a subobject path.
type PathStep struct {
	// From is the derived class of the inheritance edge.
	From string

	// To is the base class of the inheritance edge.
	To string

	// Virtual mirrors Base.Virtual for the traversed edge.
	Virtual bool
}

// Program collects the static facts produced by the analysis host.
type Program struct {
	// Classes is the full inheritance graph.
	Classes []Class

	// Facts are the pointer-flow facts: allocation points, upcasts, copies
	// and field assignments.
	Facts []Fact
}

// HierarchyError describes a malformed inheritance graph.
type HierarchyError struct {
	// Reason is a human-readable explanation.
	Reason string
}

func (e *HierarchyError) Error() string { return "typeset: " + e.Reason }

// ProgramError describes malformed pointer-flow facts.
type ProgramError struct {
	// Reason is a human-readable explanation.
	Reason string
}

func (e *ProgramError) Error() string { return "typeset: " + e.Reason }

func hierarchyError(format string, args ...any) error {
	return &HierarchyError{Reason: sprintf(format, args...)}
}

func programError(format string, args ...any) error {
	return &ProgramError{Reason: sprintf(format, args...)}
}

// subKey is the canonical identity of a subobject inside a most-derived
// object. A full non-virtual path identifies one repeated-base occurrence;
// everything at or below a virtual edge is shared, so the canonical key
// starts at the last virtual edge.
//
// The encoding is "N:a:b:c" for a non-virtual path and "V:a:b:c" for a
// shared virtual path. Class names are assumed not to contain ':'; the
// analysis host can normalize names if necessary.
type subKey string

// String returns the canonical, human-readable subobject identity.
func (k subKey) String() string { return string(k) }

// canonicalKey collapses a traversal path to its subobject identity.
func canonicalKey(path []PathStep) subKey {
	lastVirtual := -1
	for i := range path {
		if path[i].Virtual {
			lastVirtual = i
		}
	}
	switch {
	case lastVirtual >= 0:
		names := make([]string, 0, len(path)-lastVirtual)
		names = append(names, path[lastVirtual].To)
		for i := lastVirtual + 1; i < len(path); i++ {
			names = append(names, path[i].To)
		}
		return subKey("V:" + joinColon(names))
	default:
		if len(path) == 0 {
			return ""
		}
		names := make([]string, 0, len(path)+1)
		names = append(names, path[0].From)
		for _, step := range path {
			names = append(names, step.To)
		}
		return subKey("N:" + joinColon(names))
	}
}

func joinColon(parts []string) string {
	out := ""
	for i, p := range parts {
		if i > 0 {
			out += ":"
		}
		out += p
	}
	return out
}

// decodeKey renders a canonical key as the path steps of one representative
// traversal. Virtual edges cannot be reconstructed after canonicalization;
// callers needing the precise edges keep the original Origin.Path.
func decodeKey(k subKey) []PathStep {
	names := splitKey(k)
	if len(names) <= 1 {
		return nil
	}
	virtual := len(k) > 0 && k[0] == 'V'
	steps := make([]PathStep, 0, len(names)-1)
	for i := 1; i < len(names); i++ {
		steps = append(steps, PathStep{From: names[i-1], To: names[i], Virtual: virtual})
	}
	return steps
}

func splitKey(k subKey) []string {
	if k == "" {
		return nil
	}
	s := string(k)
	// Skip the N:/V: prefix.
	if len(s) >= 2 && (s[0] == 'N' || s[0] == 'V') && s[1] == ':' {
		s = s[2:]
	}
	var out []string
	start := 0
	for i := 0; i <= len(s); i++ {
		if i == len(s) || s[i] == ':' {
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	return out
}
