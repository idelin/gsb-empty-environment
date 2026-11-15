package budget

// Limits describes the four budgets enforced while unfolding a tree. Every
// field must be greater than zero; zero/negative values are rejected rather
// than silently treated as "unlimited" so that a missing budget can never
// disable enforcement.
type Limits struct {
	// MaxTotalBytes bounds the sum of bytes actually accepted from every leaf
	// Decode callback. Declared sizes are ignored when enforcing this budget.
	MaxTotalBytes int64

	// MaxDepth bounds the number of node-to-node edges on any root-to-leaf
	// path. The root node sits at depth 0, so MaxDepth 0 allows only a root
	// leaf.
	MaxDepth int

	// MaxLeaves bounds the number of leaf references visited. A shared leaf
	// node referenced N times consumes N slots.
	MaxLeaves int

	// MaxLiveBuffers bounds how many leaf staging buffers may be in flight at
	// the same time. It is the concurrency cap for Decode callbacks.
	MaxLiveBuffers int
}

func (l Limits) validate() error {
	if l.MaxTotalBytes <= 0 || l.MaxDepth < 0 || l.MaxLeaves <= 0 || l.MaxLiveBuffers <= 0 {
		return ErrInvalidConfig
	}
	return nil
}

// Validate reports whether the limits are usable. Importers such as the
// preview service call it at construction time so misconfiguration fails
// loudly instead of silently disabling enforcement.
func (l Limits) Validate() error {
	return l.validate()
}
