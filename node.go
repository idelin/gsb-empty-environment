package crush

import (
	"math"
	"strconv"
	"strings"
)

// Node is one vertex in a CRUSH topology tree.
//
// A leaf is a node without Children. In the usual topology leaves are OSDs.
// Reweight is the runtime multiplier: 1 means the static weight is unchanged
// and 0 drains the node. NewNode/NewOSD default it to 1; if an entire tree
// omits positive Reweight values, Mapper uses 1 as the implicit default.
// SetUp/SetDown and SetReweight provide explicit runtime overrides.
type Node struct {
	ID           int     `json:"id"`
	Name         string  `json:"name,omitempty"`
	Type         string  `json:"type,omitempty"`
	Kind         string  `json:"kind,omitempty"`
	Weight       float64 `json:"weight"`
	WeightV      float64 `json:"weight_v,omitempty"`
	StaticW      float64 `json:"static_w,omitempty"`
	StaticWeight float64 `json:"static_weight,omitempty"`
	Reweight     float64 `json:"reweight"`
	ReWeight     float64 `json:"re_weight,omitempty"`
	ReweightR    float64 `json:"reweight_ratio,omitempty"`
	Up           bool    `json:"up"`
	Online       bool    `json:"online,omitempty"`
	Healthy      bool    `json:"healthy,omitempty"`
	Available    bool    `json:"available,omitempty"`
	Down         bool    `json:"down,omitempty"`
	Status       string  `json:"status,omitempty"`
	State        string  `json:"state,omitempty"`
	Children     []*Node `json:"children,omitempty"`
	Nodes        []*Node `json:"nodes,omitempty"`
	Items        []*Node `json:"items,omitempty"`
	Buckets      []*Node `json:"buckets,omitempty"`
	ChildNodes   []*Node `json:"child_nodes,omitempty"`
}

// NodeType returns the configured failure-domain/device type.
func (n *Node) NodeType() string {
	if n == nil {
		return ""
	}
	if n.Type != "" {
		return n.Type
	}
	return n.Kind
}

// IsLeaf reports whether n has no child buckets.
func (n *Node) IsLeaf() bool {
	return n == nil || len(n.configuredChildren()) == 0
}

// IDString returns the decimal node id.
func (n *Node) IDString() string {
	if n == nil {
		return ""
	}
	return strconv.Itoa(n.ID)
}

// IsUp reports the node's own operational state. Ancestor state is checked by
// Mapper while walking a selected path.
func (n *Node) IsUp() bool {
	if n == nil {
		return false
	}

	status := strings.ToLower(strings.TrimSpace(firstNonEmpty(n.Status, n.State)))
	switch status {
	case "down", "offline", "out", "deleted", "destroyed":
		return false
	case "up", "in", "online", "running":
		return true
	}

	if n.Down {
		return false
	}
	return n.Up || n.Online || n.Healthy || n.Available
}

// StaticWeightValue returns the first populated static-weight alias.
func (n *Node) StaticWeightValue() float64 {
	if n == nil {
		return 0
	}
	for _, w := range []float64{n.Weight, n.StaticWeight, n.WeightV, n.StaticW} {
		if w != 0 {
			return w
		}
	}
	return 0
}

// ReweightRatio returns the runtime multiplier. A zero value means an
// explicitly drained node; constructors initialize normal nodes to one.
func (n *Node) ReweightRatio() float64 {
	if n == nil {
		return 0
	}
	for _, r := range []float64{n.Reweight, n.ReWeight, n.ReweightR} {
		if r != 0 {
			return r
		}
	}
	return 0
}

// EffectiveWeight is static weight multiplied by runtime reweight. Operational
// state intentionally does not zero the weight: CRUSH can draw a down item and
// then account for the down/retry event. Mapper applies explicit overrides on
// top of this value.
func (n *Node) EffectiveWeight() float64 {
	if n == nil {
		return 0
	}
	w := n.StaticWeightValue()
	r := n.ReweightRatio()
	if math.IsNaN(w) || math.IsInf(w, 0) || w <= 0 || r <= 0 || math.IsNaN(r) || math.IsInf(r, 0) {
		return 0
	}
	return w * r
}

// FindChild returns the first direct child with id.
func (n *Node) FindChild(id int) *Node {
	if n == nil {
		return nil
	}
	for _, child := range n.configuredChildren() {
		if child != nil && child.ID == id {
			return child
		}
	}
	return nil
}

// FindNode performs a depth-first search for id.
func (n *Node) FindNode(id int) *Node {
	if n == nil {
		return nil
	}
	if n.ID == id {
		return n
	}
	for _, child := range n.configuredChildren() {
		if found := child.FindNode(id); found != nil {
			return found
		}
	}
	return nil
}

// PathTo returns the inclusive path from n to id, or nil if absent.
func (n *Node) PathTo(id int) []*Node {
	if n == nil {
		return nil
	}
	if n.ID == id {
		return []*Node{n}
	}
	for _, child := range n.configuredChildren() {
		if path := child.PathTo(id); path != nil {
			return append([]*Node{n}, path...)
		}
	}
	return nil
}

// Walk calls fn for n and every descendant.
func (n *Node) Walk(fn func(*Node)) {
	if n == nil || fn == nil {
		return
	}
	fn(n)
	for _, child := range n.configuredChildren() {
		child.Walk(fn)
	}
}

func (n *Node) configuredChildren() []*Node {
	if n == nil {
		return nil
	}
	for _, children := range [][]*Node{n.Children, n.ChildNodes, n.Nodes, n.Items, n.Buckets} {
		if len(children) > 0 {
			return children
		}
	}
	return nil
}

// Clone returns a deep copy of the topology.
func (n *Node) Clone() *Node {
	if n == nil {
		return nil
	}
	copyNode := *n
	copyNode.Children = cloneNodeSlice(n.Children)
	copyNode.Nodes = cloneNodeSlice(n.Nodes)
	copyNode.Items = cloneNodeSlice(n.Items)
	copyNode.Buckets = cloneNodeSlice(n.Buckets)
	copyNode.ChildNodes = cloneNodeSlice(n.ChildNodes)
	return &copyNode
}

func cloneNodeSlice(in []*Node) []*Node {
	if in == nil {
		return nil
	}
	out := make([]*Node, len(in))
	for i, child := range in {
		out[i] = child.Clone()
	}
	return out
}

// NewNode is a small topology constructor.
func NewNode(id int, nodeType string, weight float64, children ...*Node) *Node {
	return &Node{
		ID:       id,
		Type:     nodeType,
		Weight:   weight,
		Reweight: 1,
		Up:       true,
		Children: children,
	}
}

// NewOSD constructs an up OSD leaf with normal reweight.
func NewOSD(id int, weight float64) *Node {
	return NewNode(id, "osd", weight)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
