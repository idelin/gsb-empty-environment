package netsec

import (
	"fmt"
	"math"
	"strings"

	"gopkg.in/yaml.v3"
)

// Limits caps the estimated work produced by aliases and merge keys. A
// non-positive field disables that individual limit, which is useful when a
// caller only wants to constrain one dimension.
type Limits struct {
	MaxNodes      int
	MaxDepth      int
	MaxMergeDepth int
}

// DefaultLimits returns conservative limits suitable for pipeline
// configuration. They reject exponential alias bombs while leaving ample room
// for ordinary shared configuration blocks.
func DefaultLimits() Limits {
	return Limits{
		MaxNodes:      20000,
		MaxDepth:      100,
		MaxMergeDepth: 8,
	}
}

// Stats is the pre-expansion estimate for a Node graph. Nodes, Depth, and
// MergeDepth are upper bounds on the traversal/materialization work of a full
// yaml.v3 decode. Aliases are transparent for node counting and depth, because
// the expanded value replaces the alias node.
type Stats struct {
	Nodes      int
	Depth      int
	MergeDepth int
}

func (s Stats) String() string {
	return fmt.Sprintf("nodes=%d depth=%d merge-depth=%d", s.Nodes, s.Depth, s.MergeDepth)
}

// Budget validates a yaml.v3 Node tree without expanding aliases. The same
// Budget may be reused for multiple documents; no document state is retained.
type Budget struct {
	Limits Limits

	// MaxNodes, MaxDepth, and MaxMergeDepth are convenience overrides. When
	// positive they take precedence over the corresponding Limits field.
	MaxNodes      int
	MaxDepth      int
	MaxMergeDepth int
}

// NewBudget returns a budget using limits.
func NewBudget(limits Limits) *Budget {
	return &Budget{
		Limits:        limits,
		MaxNodes:      limits.MaxNodes,
		MaxDepth:      limits.MaxDepth,
		MaxMergeDepth: limits.MaxMergeDepth,
	}
}

// NewDefaultBudget returns a budget using DefaultLimits.
func NewDefaultBudget() *Budget {
	return NewBudget(DefaultLimits())
}

// Analyze estimates the expanded node count, depth, and merge-key nesting and
// enforces the budget. The returned Stats are populated when a limit error is
// returned; cycle and malformed-graph errors abort before an estimate exists.
func (b *Budget) Analyze(root *yaml.Node) (Stats, error) {
	if root == nil {
		return Stats{}, &MalformedError{Detail: "nil root node"}
	}
	if b == nil {
		return Stats{}, fmt.Errorf("netsec: nil budget")
	}
	w := &walker{
		state: make(map[*yaml.Node]walkState),
		memo:  make(map[*yaml.Node]estimate),
	}
	estimate, err := w.inspect(root)
	if err != nil {
		return estimate.Stats, err
	}
	stats := estimate.Stats
	limits := b.effectiveLimits()

	if limit := limits.MaxNodes; limit > 0 && stats.Nodes > limit {
		return stats, &LimitError{
			Resource: ResourceNodes,
			Limit:    limit,
			Actual:   stats.Nodes,
			Stats:    stats,
			Line:     estimate.NodeLine,
			Column:   estimate.NodeColumn,
		}
	}
	if limit := limits.MaxDepth; limit > 0 && stats.Depth > limit {
		return stats, &LimitError{
			Resource: ResourceDepth,
			Limit:    limit,
			Actual:   stats.Depth,
			Stats:    stats,
			Line:     estimate.DepthLine,
			Column:   estimate.DepthColumn,
		}
	}
	if limit := limits.MaxMergeDepth; limit > 0 && stats.MergeDepth > limit {
		return stats, &LimitError{
			Resource: ResourceMergeDepth,
			Limit:    limit,
			Actual:   stats.MergeDepth,
			Stats:    stats,
			Line:     estimate.MergeLine,
			Column:   estimate.MergeColumn,
		}
	}
	return stats, nil
}

func (b *Budget) effectiveLimits() Limits {
	limits := b.Limits
	if b.MaxNodes > 0 {
		limits.MaxNodes = b.MaxNodes
	}
	if b.MaxDepth > 0 {
		limits.MaxDepth = b.MaxDepth
	}
	if b.MaxMergeDepth > 0 {
		limits.MaxMergeDepth = b.MaxMergeDepth
	}
	return limits
}

// Check enforces the budget and discards the estimate on success.
func (b *Budget) Check(root *yaml.Node) error {
	_, err := b.Analyze(root)
	return err
}

// Inspect is an alias for Analyze.
func (b *Budget) Inspect(root *yaml.Node) (Stats, error) {
	return b.Analyze(root)
}

// Estimate is an alias for Analyze.
func (b *Budget) Estimate(root *yaml.Node) (Stats, error) {
	return b.Analyze(root)
}

// Check validates root against limits. With no limits argument it uses
// DefaultLimits.
func Check(root *yaml.Node, limits ...Limits) error {
	_, err := Analyze(root, limits...)
	return err
}

// Validate is an alias for Check.
func Validate(root *yaml.Node, limits ...Limits) error {
	return Check(root, limits...)
}

// Analyze estimates the expanded work for root. With no limits argument it
// uses DefaultLimits.
func Analyze(root *yaml.Node, limits ...Limits) (Stats, error) {
	return budgetFromArgs(limits).Analyze(root)
}

// Inspect is an alias for Analyze.
func Inspect(root *yaml.Node, limits ...Limits) (Stats, error) {
	return Analyze(root, limits...)
}

// Estimate is an alias for Analyze.
func Estimate(root *yaml.Node, limits ...Limits) (Stats, error) {
	return Analyze(root, limits...)
}

// CheckLimits is a positional convenience wrapper.
func CheckLimits(root *yaml.Node, maxNodes, maxDepth, maxMergeDepth int) error {
	return NewBudget(Limits{
		MaxNodes:      maxNodes,
		MaxDepth:      maxDepth,
		MaxMergeDepth: maxMergeDepth,
	}).Check(root)
}

func budgetFromArgs(limits []Limits) *Budget {
	if len(limits) == 0 {
		return NewDefaultBudget()
	}
	return NewBudget(limits[0])
}

type walkState uint8

const (
	stateVisiting walkState = 1
	stateDone     walkState = 2
)

type walker struct {
	state map[*yaml.Node]walkState
	memo  map[*yaml.Node]estimate
	stack []*yaml.Node
}

type estimate struct {
	Stats
	NodeLine, NodeColumn   int
	DepthLine, DepthColumn int
	MergeLine, MergeColumn int
	maxChildNodes          int
}

func scalarEstimate(node *yaml.Node) estimate {
	return estimate{
		Stats:       Stats{Nodes: 1, Depth: 1},
		NodeLine:    node.Line,
		NodeColumn:  node.Column,
		DepthLine:   node.Line,
		DepthColumn: node.Column,
		MergeLine:   node.Line,
		MergeColumn: node.Column,
	}
}

func (w *walker) inspect(node *yaml.Node) (estimate, error) {
	if node == nil {
		return estimate{}, &MalformedError{Detail: "nil node in content"}
	}
	switch w.state[node] {
	case stateDone:
		return w.memo[node], nil
	case stateVisiting:
		return estimate{}, w.cycleError(node)
	}

	w.state[node] = stateVisiting
	w.stack = append(w.stack, node)
	defer func() {
		w.stack = w.stack[:len(w.stack)-1]
	}()

	result, err := w.inspectNode(node)
	if err != nil {
		return result, err
	}
	w.state[node] = stateDone
	w.memo[node] = result
	return result, nil
}

func (w *walker) inspectNode(node *yaml.Node) (estimate, error) {
	if node.Kind == yaml.AliasNode {
		if node.Alias == nil {
			return estimate{}, &InvalidAliasError{Alias: node, Line: node.Line}
		}
		target, err := w.inspect(node.Alias)
		if err != nil {
			return target, err
		}
		target.NodeLine = node.Line
		target.NodeColumn = node.Column
		target.DepthLine = node.Line
		target.DepthColumn = node.Column
		target.MergeLine = node.Line
		target.MergeColumn = node.Column
		return target, nil
	}

	switch node.Kind {
	case yaml.ScalarNode:
		return scalarEstimate(node), nil
	case yaml.DocumentNode:
		if len(node.Content) > 1 {
			return estimate{}, &MalformedError{Line: node.Line, Detail: "document has multiple roots"}
		}
		return w.inspectChildren(node, node.Content)
	case yaml.SequenceNode:
		return w.inspectChildren(node, node.Content)
	case yaml.MappingNode:
		return w.inspectMapping(node)
	default:
		return estimate{}, &MalformedError{Line: node.Line, Detail: fmt.Sprintf("unsupported node kind %d", node.Kind)}
	}
}

func (w *walker) inspectChildren(node *yaml.Node, children []*yaml.Node) (estimate, error) {
	stats := scalarEstimate(node)
	for _, child := range children {
		childEstimate, err := w.inspect(child)
		if err != nil {
			return stats, err
		}
		stats.addChild(childEstimate, false, nil)
	}
	return stats, nil
}

func (w *walker) inspectMapping(node *yaml.Node) (estimate, error) {
	if len(node.Content)%2 != 0 {
		return estimate{}, &MalformedError{Line: node.Line, Detail: "mapping has an odd number of content nodes"}
	}
	stats := scalarEstimate(node)
	for i := 0; i < len(node.Content); i += 2 {
		key := node.Content[i]
		value := node.Content[i+1]
		keyEstimate, err := w.inspect(key)
		if err != nil {
			return stats, err
		}
		valueEstimate, err := w.inspect(value)
		if err != nil {
			return stats, err
		}
		stats.addChild(keyEstimate, false, nil)
		stats.addChild(valueEstimate, isMergeKeyNode(key), key)
	}
	return stats, nil
}

func (e *estimate) addChild(child estimate, mergeEdge bool, mergeKey *yaml.Node) {
	e.Nodes = saturatingAdd(e.Nodes, child.Nodes)
	if child.Nodes > e.maxChildNodes {
		e.maxChildNodes = child.Nodes
		e.NodeLine = child.NodeLine
		e.NodeColumn = child.NodeColumn
	}
	if candidate := saturatingAdd(1, child.Depth); candidate > e.Depth {
		e.Depth = candidate
		e.DepthLine = child.DepthLine
		e.DepthColumn = child.DepthColumn
	}
	if mergeEdge {
		if candidate := saturatingAdd(1, child.MergeDepth); candidate > e.MergeDepth {
			e.MergeDepth = candidate
			e.MergeLine = mergeKey.Line
			e.MergeColumn = mergeKey.Column
		}
	} else if child.MergeDepth > e.MergeDepth {
		e.MergeDepth = child.MergeDepth
		e.MergeLine = child.MergeLine
		e.MergeColumn = child.MergeColumn
	}
}

func (w *walker) cycleError(target *yaml.Node) *CycleError {
	cycle := &CycleError{
		Anchor: target.Anchor,
		Line:   target.Line,
		Column: target.Column,
	}
	for i := len(w.stack) - 1; i >= 0; i-- {
		node := w.stack[i]
		cycle.Path = append(cycle.Path, node)
		if node == target {
			break
		}
	}
	if cycle.Anchor == "" && len(cycle.Path) > 0 {
		cycle.Anchor = cycle.Path[0].Anchor
	}
	for i := len(cycle.Path) - 1; i >= 0; i-- {
		if cycle.Path[i].Kind == yaml.AliasNode {
			cycle.Alias = cycle.Path[i]
			if cycle.Path[i].Line > 0 {
				cycle.Line = cycle.Path[i].Line
				cycle.Column = cycle.Path[i].Column
			}
			if cycle.Path[i].Value != "" {
				cycle.Anchor = cycle.Path[i].Value
			}
			break
		}
	}
	return cycle
}

// isMergeKeyNode recognizes the YAML merge key while avoiding quoted "<<" keys.
// yaml.v3 tags a plain << as !!merge; manually constructed Nodes commonly
// leave Tag empty, so both forms are accepted.
func isMergeKeyNode(node *yaml.Node) bool {
	if node == nil || node.Kind != yaml.ScalarNode || node.Value != "<<" {
		return false
	}
	tag := node.Tag
	if strings.HasPrefix(tag, "tag:yaml.org,2002:") {
		tag = "!!" + strings.TrimPrefix(tag, "tag:yaml.org,2002:")
	}
	if tag != "" && tag != "!!merge" {
		return false
	}
	return node.Style&yaml.DoubleQuotedStyle == 0 && node.Style&yaml.SingleQuotedStyle == 0
}

func saturatingAdd(a, b int) int {
	if b > 0 && a > math.MaxInt-b {
		return math.MaxInt
	}
	return a + b
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
