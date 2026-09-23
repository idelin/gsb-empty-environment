package objectgraph

import "errors"

// Sentinel errors returned by the codec and the graph analyzer. Callers can
// inspect them with errors.Is. Decode never returns a partially built Value
// together with an error: on failure the caller only gets an error value.
var (
	ErrBadLimits      = errors.New("objectgraph: invalid limits")
	ErrTruncated      = errors.New("objectgraph: truncated input")
	ErrBadVersion     = errors.New("objectgraph: unsupported format version")
	ErrBadTag         = errors.New("objectgraph: unknown node tag")
	ErrBadLength      = errors.New("objectgraph: length exceeds limit or remaining input")
	ErrNodeLimit      = errors.New("objectgraph: node count exceeds limit")
	ErrRefLimit       = errors.New("objectgraph: total reference count exceeds budget")
	ErrBadRef         = errors.New("objectgraph: reference index out of range")
	ErrUnreachable    = errors.New("objectgraph: node not reachable from root")
	ErrDepth          = errors.New("objectgraph: condensation depth exceeds limit")
	ErrCyclicNodes    = errors.New("objectgraph: cyclic node count exceeds separate quota")
	ErrPendingRefs    = errors.New("objectgraph: pending forward-reference count exceeds cap")
	ErrDanglingRef    = errors.New("objectgraph: forward references remain unresolved")
	ErrRootIndex      = errors.New("objectgraph: root index out of range")
	ErrEncoderInvalid = errors.New("objectgraph: invalid encoder input")
)

// LimitError marks errors caused by a declared length that would force an
// allocation before the input was proven to contain the claimed bytes.
type LimitError struct {
	Kind string
}

func (e *LimitError) Error() string { return "objectgraph: length rejected: " + e.Kind }
func (e *LimitError) Is(target error) bool {
	return target == ErrBadLength
}
