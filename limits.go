package graphdec

// Limits jointly bound a single Decode call. A zero field selects the
// documented default, so the zero Limits value is fully usable.
type Limits struct {
	// MaxBytes bounds the total encoded input size. Default 1 MiB.
	MaxBytes int
	// MaxNodes bounds the number of distinct nodes. Shared nodes are
	// counted once, no matter how many times they are referenced.
	// Default 65536.
	MaxNodes int
	// MaxRefs bounds the total number of references (edges), so even a
	// tiny input cannot declare unbounded fan-out. Default 131072.
	MaxRefs int
	// MaxDepth bounds the longest path of the SCC-condensation DAG
	// reachable from the root. Cycles never inflate depth. Default 256.
	MaxDepth int
	// MaxCycleNodes bounds how many nodes may participate in cycles
	// (SCCs of size > 1, plus self-loops). Default 1024.
	MaxCycleNodes int
	// MaxPendingRefs bounds how many distinct forward references may be
	// staged, unresolved, at any moment during incremental decoding.
	// Default 16384.
	MaxPendingRefs int
}

const (
	defaultMaxBytes       = 1 << 20
	defaultMaxNodes       = 1 << 16
	defaultMaxRefs        = 1 << 17
	defaultMaxDepth       = 256
	defaultMaxCycleNodes  = 1024
	defaultMaxPendingRefs = 1 << 14
)

func (l Limits) withDefaults() Limits {
	if l.MaxBytes <= 0 {
		l.MaxBytes = defaultMaxBytes
	}
	if l.MaxNodes <= 0 {
		l.MaxNodes = defaultMaxNodes
	}
	if l.MaxRefs <= 0 {
		l.MaxRefs = defaultMaxRefs
	}
	if l.MaxDepth <= 0 {
		l.MaxDepth = defaultMaxDepth
	}
	if l.MaxCycleNodes <= 0 {
		l.MaxCycleNodes = defaultMaxCycleNodes
	}
	if l.MaxPendingRefs <= 0 {
		l.MaxPendingRefs = defaultMaxPendingRefs
	}
	return l
}

// Decoder decodes object graphs under a fixed Limits configuration.
// It keeps no state between calls: a call that fails against the limits
// leaves the Decoder fully usable for subsequent calls.
type Decoder struct {
	Limits Limits
}

// Decode parses and validates data, returning the root of the decoded
// graph. On any error the returned *Value is nil; no partial object is
// ever exposed.
func (d *Decoder) Decode(data []byte) (*Value, error) {
	return decode(data, d.Limits.withDefaults())
}

// Decode is a convenience wrapper around a Decoder with default limits.
func Decode(data []byte) (*Value, error) {
	return decode(data, Limits{}.withDefaults())
}
