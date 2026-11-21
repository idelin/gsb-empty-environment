package typeset

// Common embedding aliases.  They are type aliases, so values remain fully
// interchangeable with the primary names.
type (
	ClassInfo      = Class
	TypeInfo       = Class
	BaseClass      = Base
	MethodInfo     = Method
	FieldInfo      = Field
	AllocationSite = Allocation
	FieldStore     = FieldAssignment
	CallSite       = VirtualCall
	TypeSetResult  = Result
	VirtualTarget  = Target
	AllocationPath = Provenance
	Diagnostic     = Ambiguity
)

// AnalyzeTypes is an alias for Analyze.
func AnalyzeTypes(args ...any) (*Result, error) { return Analyze(args...) }

// ValidateHierarchy checks class names, references and inheritance cycles.
func ValidateHierarchy(classes []Class) error {
	_, err := BuildHierarchy(classes)
	return err
}

// Resolve resolves one virtual call without building allocation/field flow.
func Resolve(classes []Class, dynamic, static, method string, path ...string) (Resolution, error) {
	h, err := BuildHierarchy(classes)
	if err != nil {
		return Resolution{}, err
	}
	return h.Resolve(dynamic, static, method, path...)
}

// ResolveVirtual resolves a call through the most-derived type.
func ResolveVirtual(classes []Class, dynamic, method string, path ...string) (Resolution, error) {
	h, err := BuildHierarchy(classes)
	if err != nil {
		return Resolution{}, err
	}
	return h.Resolve(dynamic, dynamic, method, path...)
}

// CandidatesFor returns the candidate targets for a call.
func (r *Result) CandidatesFor(callID string) []Target {
	return r.TargetsForCall(callID)
}

// IsAmbiguous reports whether any call or conversion is ambiguous.
func (r *Result) IsAmbiguous() bool {
	return r != nil && len(r.Ambiguities) != 0
}
