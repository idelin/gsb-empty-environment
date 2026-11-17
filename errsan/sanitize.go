package errsan

import (
	"sort"
	"strconv"
	"strings"
)

// FailureCode is the only code emitted when no registered code is
// reachable, or when any traversal budget is exceeded. It carries no
// parameters, no stack and no private fields.
const FailureCode = "internal_failure"

// PublicError is the sanitized external view: exactly one registered
// code plus its whitelisted, type-checked parameters.
type PublicError struct {
	Code   string
	Params map[string]any
}

// Failure returns the unified failure output.
func Failure() PublicError {
	return PublicError{Code: FailureCode}
}

// Options bounds the sanitization work. Exceeding any budget yields the
// unified failure. Zero fields take the defaults below.
type Options struct {
	MaxNodes         int // distinct graph nodes visited
	MaxDepth         int // cause-chain depth
	MaxParamsPerNode int // candidate params inspected per node
	MaxStringLen     int // max length of a string param value
}

// DefaultOptions returns the budgets applied to zero-valued fields.
func DefaultOptions() Options {
	return Options{
		MaxNodes:         1024,
		MaxDepth:         256,
		MaxParamsPerNode: 32,
		MaxStringLen:     256,
	}
}

func (o Options) withDefaults() Options {
	d := DefaultOptions()
	if o.MaxNodes <= 0 {
		o.MaxNodes = d.MaxNodes
	}
	if o.MaxDepth <= 0 {
		o.MaxDepth = d.MaxDepth
	}
	if o.MaxParamsPerNode <= 0 {
		o.MaxParamsPerNode = d.MaxParamsPerNode
	}
	if o.MaxStringLen <= 0 {
		o.MaxStringLen = d.MaxStringLen
	}
	return o
}

// Sanitize folds the internal error graph rooted at root into a single
// PublicError.
//
// The result depends only on the multiset of reachable registered codes
// and their whitelisted parameters: never on private messages, private
// subgraphs, cause ordering or map iteration order. Cycles and shared
// nodes are visited once. Any budget overflow yields Failure().
func Sanitize(root *Node, reg *Registry, opts Options) PublicError {
	o := opts.withDefaults()
	if root == nil || reg == nil {
		return Failure()
	}

	type frame struct {
		node  *Node
		depth int
	}
	visited := make(map[*Node]struct{})
	stack := []frame{{node: root, depth: 0}}

	var best *candidate
	for len(stack) > 0 {
		f := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if f.node == nil {
			continue
		}
		if _, seen := visited[f.node]; seen {
			continue
		}
		if f.depth > o.MaxDepth {
			return Failure()
		}
		visited[f.node] = struct{}{}
		if len(visited) > o.MaxNodes {
			return Failure()
		}

		if spec, ok := reg.lookup(f.node.code); ok {
			params, over := sanitizeParams(f.node, spec, o)
			if over {
				return Failure()
			}
			c := candidate{spec: spec, params: params}
			if best == nil || c.less(*best) {
				best = &c
			}
		}

		for _, cause := range f.node.causes {
			stack = append(stack, frame{node: cause, depth: f.depth + 1})
		}
	}

	if best == nil {
		return Failure()
	}
	return PublicError{Code: best.spec.Code, Params: best.params}
}

// candidate is a sanitized node still in the running for selection.
type candidate struct {
	spec   CodeSpec
	params map[string]any
}

// less reports whether c beats other: higher priority first, then the
// lexicographically smaller code, then the canonically smaller parameter
// set so the outcome is independent of traversal order.
func (c candidate) less(other candidate) bool {
	if c.spec.Priority != other.spec.Priority {
		return c.spec.Priority > other.spec.Priority
	}
	if c.spec.Code != other.spec.Code {
		return c.spec.Code < other.spec.Code
	}
	return canonicalParams(c.params) < canonicalParams(other.params)
}

// sanitizeParams keeps only whitelisted parameters whose values strictly
// match the declared type. Anything else (unknown keys, wrong types,
// nested objects, oversize strings) is dropped. The second return value
// reports a param-count budget overflow.
func sanitizeParams(n *Node, spec CodeSpec, o Options) (map[string]any, bool) {
	if len(n.params) > o.MaxParamsPerNode {
		return nil, true
	}
	if len(spec.Params) == 0 || len(n.params) == 0 {
		return nil, false
	}
	var out map[string]any
	for _, ps := range spec.Params {
		v, present := n.params[ps.Name]
		if !present {
			continue
		}
		if cv, ok := convertValue(ps.Type, v, o.MaxStringLen); ok {
			if out == nil {
				out = make(map[string]any, len(spec.Params))
			}
			out[ps.Name] = cv
		}
	}
	return out, false
}

// convertValue strictly validates a parameter value. Only plain strings,
// ints and bools are accepted; object references, slices, maps, floats
// and pointers never pass.
func convertValue(t ParamType, v any, maxStr int) (any, bool) {
	switch t {
	case ParamString:
		s, ok := v.(string)
		if !ok || len(s) > maxStr {
			return nil, false
		}
		return s, true
	case ParamInt:
		switch n := v.(type) {
		case int:
			return int64(n), true
		case int64:
			return n, true
		}
		return nil, false
	case ParamBool:
		b, ok := v.(bool)
		if !ok {
			return nil, false
		}
		return b, true
	}
	return nil, false
}

// canonicalParams renders a parameter set in a fully deterministic form
// for order-independent comparison.
func canonicalParams(params map[string]any) string {
	if len(params) == 0 {
		return ""
	}
	keys := make([]string, 0, len(params))
	for k := range params {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, k := range keys {
		b.WriteString(k)
		b.WriteByte(0)
		switch v := params[k].(type) {
		case string:
			b.WriteByte('s')
			b.WriteString(v)
		case int64:
			b.WriteByte('i')
			b.WriteString(strconv.FormatInt(v, 10))
		case bool:
			b.WriteByte('b')
			b.WriteString(strconv.FormatBool(v))
		}
		b.WriteByte(0)
	}
	return b.String()
}
