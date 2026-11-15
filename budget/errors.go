package budget

import "errors"

// Sentinel errors returned by the unfold engine. Callers should use errors.Is
// rather than matching message text.
var (
	// ErrInvalidConfig is returned when a Limits value is unusable.
	ErrInvalidConfig = errors.New("budget: invalid limits configuration")

	// ErrInvalidTree is returned when the supplied node tree is malformed.
	ErrInvalidTree = errors.New("budget: invalid node tree")

	// ErrTotalBytesExceeded is returned when a Write would push the actual
	// decoded byte total past MaxTotalBytes. The offending Write is rejected
	// before any of its bytes land in a buffer.
	ErrTotalBytesExceeded = errors.New("budget: total decoded byte budget exceeded")

	// ErrDepthExceeded is returned when a root-to-leaf path is deeper than
	// MaxDepth.
	ErrDepthExceeded = errors.New("budget: node depth budget exceeded")

	// ErrLeafLimitExceeded is returned when more than MaxLeaves leaf
	// references are visited. Each reference to a shared leaf counts.
	ErrLeafLimitExceeded = errors.New("budget: leaf count budget exceeded")

	// ErrCycle is returned as soon as a node references one of its own
	// ancestors.
	ErrCycle = errors.New("budget: cyclic node reference")

	// ErrShortWrite is returned when an underlying staging sink accepts fewer
	// bytes than offered without reporting an error of its own.
	ErrShortWrite = errors.New("budget: short write while staging decoded bytes")
)
