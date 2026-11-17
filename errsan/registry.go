package errsan

import "errors"

// ParamType is the strictly validated type of a public parameter.
type ParamType int

const (
	ParamString ParamType = iota
	ParamInt
	ParamBool
)

// ParamSpec declares one whitelisted public parameter of an error code.
type ParamSpec struct {
	Name string
	Type ParamType
}

// CodeSpec declares a public error code, its selection priority and its
// public parameter whitelist. Higher Priority wins; ties are broken by
// the lexicographically smaller code.
type CodeSpec struct {
	Code     string
	Priority int
	Params   []ParamSpec
}

// Registry is the allowlist of error codes that may appear in external
// output. Build one with NewRegistry.
type Registry struct {
	specs map[string]CodeSpec
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry {
	return &Registry{specs: make(map[string]CodeSpec)}
}

// Register adds a code spec. It rejects empty codes, duplicate codes,
// duplicate parameter names and the reserved failure code.
func (r *Registry) Register(spec CodeSpec) error {
	if spec.Code == "" {
		return errors.New("errsan: empty code")
	}
	if spec.Code == FailureCode {
		return errors.New("errsan: code reserved for unified failure")
	}
	if _, dup := r.specs[spec.Code]; dup {
		return errors.New("errsan: duplicate code " + spec.Code)
	}
	seen := make(map[string]struct{}, len(spec.Params))
	for _, p := range spec.Params {
		if p.Name == "" {
			return errors.New("errsan: empty param name")
		}
		if p.Type != ParamString && p.Type != ParamInt && p.Type != ParamBool {
			return errors.New("errsan: unknown param type for " + p.Name)
		}
		if _, dup := seen[p.Name]; dup {
			return errors.New("errsan: duplicate param " + p.Name)
		}
		seen[p.Name] = struct{}{}
	}
	r.specs[spec.Code] = spec
	return nil
}

func (r *Registry) lookup(code string) (CodeSpec, bool) {
	if r == nil {
		return CodeSpec{}, false
	}
	spec, ok := r.specs[code]
	return spec, ok
}
