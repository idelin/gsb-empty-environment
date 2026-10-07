package perflib

// CallNode is one executed call frame in a thread's call tree.
type CallNode struct {
	Name     string
	Thread   uint64
	Start    int64
	End      int64
	Parent   *CallNode
	Children []*CallNode
}

// Inclusive is the wall-clock span of the frame, children included.
func (n *CallNode) Inclusive() int64 { return n.End - n.Start }

// Exclusive is the span minus the inclusive time of direct children,
// i.e. time slices where this frame was the top of its thread stack.
func (n *CallNode) Exclusive() int64 {
	ex := n.Inclusive()
	for _, c := range n.Children {
		ex -= c.Inclusive()
	}
	if ex < 0 {
		ex = 0
	}
	return ex
}

// Interval is a half-open time span [Start, End).
type Interval struct {
	Start int64
	End   int64
}

// Span returns the frame's interval.
func (n *CallNode) Span() Interval { return Interval{Start: n.Start, End: n.End} }

// Walk visits every node in the forest, depth first.
func Walk(roots []*CallNode, visit func(*CallNode)) {
	for _, r := range roots {
		walk(r, visit)
	}
}

func walk(n *CallNode, visit func(*CallNode)) {
	visit(n)
	for _, c := range n.Children {
		walk(c, visit)
	}
}
