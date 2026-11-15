package netsec

import "fmt"

// ParamKind is the strictly validated type of a public parameter.
type ParamKind int

const (
	// KindString accepts only Go strings, bounded by ParamSpec.MaxLen.
	KindString ParamKind = iota
	// KindInt accepts only Go integer types (normalized to int64).
	// Floats are rejected even when integral.
	KindInt
	// KindBool accepts only Go bools.
	KindBool
)

// DefaultMaxStringLen bounds KindString values when ParamSpec.MaxLen <= 0.
const DefaultMaxStringLen = 256

// ParamSpec declares one whitelisted public parameter of an error code.
type ParamSpec struct {
	Name string
	Kind ParamKind
	// MaxLen bounds KindString values in bytes. Values <= 0 use
	// DefaultMaxStringLen. Ignored for other kinds.
	MaxLen int
}

// CodeSpec declares one externally visible error code.
type CodeSpec struct {
	Code string
	// Priority decides between competing codes found in one graph:
	// higher wins; ties break to the lexicographically smaller code.
	Priority int
	// Params is the public parameter whitelist for this code. Any
	// parameter not listed here is stripped from external output.
	Params []ParamSpec
}

// Registry is the allowlist of error codes and typed parameters that may
// appear in external output. It is safe for concurrent use.
type Registry struct {
	specs map[string]CodeSpec
}

// NewRegistry validates specs and returns a Registry. It rejects empty
// or duplicate codes and empty or duplicate parameter names so the
// allowlist itself can never be ambiguous.
func NewRegistry(specs ...CodeSpec) (*Registry, error) {
	r := &Registry{specs: make(map[string]CodeSpec, len(specs))}
	for _, spec := range specs {
		if spec.Code == "" {
			return nil, fmt.Errorf("netsec: empty error code in registry")
		}
		if _, dup := r.specs[spec.Code]; dup {
			return nil, fmt.Errorf("netsec: duplicate error code %q", spec.Code)
		}
		seen := make(map[string]struct{}, len(spec.Params))
		for _, p := range spec.Params {
			if p.Name == "" {
				return nil, fmt.Errorf("netsec: empty param name for code %q", spec.Code)
			}
			if _, dup := seen[p.Name]; dup {
				return nil, fmt.Errorf("netsec: duplicate param %q for code %q", p.Name, spec.Code)
			}
			seen[p.Name] = struct{}{}
		}
		r.specs[spec.Code] = spec
	}
	return r, nil
}

// validateParam strictly checks v against spec. Anything outside the
// declared kind — including embedded objects, maps, slices, pointers and
// floats for KindInt — is rejected.
func validateParam(spec ParamSpec, v any) (any, bool) {
	switch spec.Kind {
	case KindString:
		s, ok := v.(string)
		if !ok {
			return nil, false
		}
		max := spec.MaxLen
		if max <= 0 {
			max = DefaultMaxStringLen
		}
		if len(s) > max {
			return nil, false
		}
		return s, true
	case KindInt:
		switch n := v.(type) {
		case int:
			return int64(n), true
		case int8:
			return int64(n), true
		case int16:
			return int64(n), true
		case int32:
			return int64(n), true
		case int64:
			return n, true
		case uint:
			return int64(n), true
		case uint8:
			return int64(n), true
		case uint16:
			return int64(n), true
		case uint32:
			return int64(n), true
		case uint64:
			if n > uint64(^uint64(0)>>1) {
				return nil, false
			}
			return int64(n), true
		default:
			return nil, false
		}
	case KindBool:
		b, ok := v.(bool)
		if !ok {
			return nil, false
		}
		return b, true
	default:
		return nil, false
	}
}
