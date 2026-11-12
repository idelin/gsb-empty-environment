package netsec

// Source identifies the trust class of a value origin.
type Source int

const (
	// Trusted marks content authored into a trusted launcher template.
	Trusted Source = iota
	// Untrusted marks content inherited from the ambient process environment.
	Untrusted
)

// String returns "trusted" or "untrusted".
func (s Source) String() string {
	switch s {
	case Trusted:
		return "trusted"
	case Untrusted:
		return "untrusted"
	default:
		return "unknown"
	}
}

// Origin records where one contiguous run of output bytes came from.
type Origin struct {
	Source Source
	// Name is the variable that contributed the run. For template literals it
	// is the variable whose template carries the literal; for an inherited
	// value it is the imported variable itself.
	Name string
}

// Run is a contiguous, provenance-tagged slice of an expanded value.
type Run struct {
	Origin Origin
	Data   string
}

// Value is a fully expanded variable together with its per-byte provenance.
type Value struct {
	Name string
	Data string
	Runs []Run
}

// OriginAt reports the origin of the byte at index i. It panics if i is out
// of range.
func (v Value) OriginAt(i int) Origin {
	if i < 0 || i >= len(v.Data) {
		panic("netsec: byte index out of range")
	}
	for _, run := range v.Runs {
		if i < len(run.Data) {
			return run.Origin
		}
		i -= len(run.Data)
	}
	panic("netsec: provenance runs do not cover value")
}

// AllTrusted reports whether every byte of the value has a trusted origin.
// An empty value is vacuously trusted.
func (v Value) AllTrusted() bool {
	for _, run := range v.Runs {
		if run.Origin.Source != Trusted {
			return false
		}
	}
	return true
}

// Budget bounds the expansion work performed by one Build call.
type Budget struct {
	// MaxOutputBytes bounds the cumulative number of bytes appended while
	// expanding every variable, including bytes re-copied when a shared
	// sub-expression is referenced more than once.
	MaxOutputBytes int
	// MaxDepth bounds the longest ${NAME} reference chain followed.
	MaxDepth int
}

// DefaultBudget is applied when no explicit budget is configured.
var DefaultBudget = Budget{MaxOutputBytes: 1 << 20, MaxDepth: 64}

// Spec declares one variable from a trusted launcher template.
type Spec struct {
	// Name is the exported environment variable name.
	Name string
	// Template is the trusted definition. It may contain literal bytes,
	// ${NAME} references and the "$$" escape.
	Template string
	// Sensitive requires every final byte of the expanded value to be trusted.
	Sensitive bool
}

// Stats reports expansion work measured during Build.
type Stats struct {
	// Expansions is the number of distinct variables actually expanded.
	Expansions int
	// CacheHits is the number of times a shared sub-expression was reused.
	CacheHits int
	// MaxDepthObserved is the longest reference chain followed.
	MaxDepthObserved int
	// OutputBytes is the cumulative bytes appended across all expansions.
	OutputBytes int
}
