package netsec

// FailureCode is the single unified external code used whenever the
// graph contains no registered code, the budget is exceeded, or the
// input is unusable. It deliberately carries no parameters, no message
// and no stack information.
const FailureCode = "INTERNAL_FAILURE"

// Budget bounds the work Sanitize may perform. Exceeding any limit
// yields the unified failure instead of a partial result.
type Budget struct {
	// MaxNodes bounds the number of distinct nodes visited.
	MaxNodes int
	// MaxDepth bounds cause-chain depth from the root.
	MaxDepth int
}

// DefaultBudget is used for zero or negative Budget fields.
var DefaultBudget = Budget{MaxNodes: 1024, MaxDepth: 64}

func (b Budget) withDefaults() Budget {
	if b.MaxNodes <= 0 {
		b.MaxNodes = DefaultBudget.MaxNodes
	}
	if b.MaxDepth <= 0 {
		b.MaxDepth = DefaultBudget.MaxDepth
	}
	return b
}

// PublicError is the only value that may cross the trust boundary. It
// contains a registered code and whitelisted, strictly typed parameters
// — never private messages, causes, or stack data.
type PublicError struct {
	Code   string         `json:"code"`
	Params map[string]any `json:"params,omitempty"`
}

// Error returns only the public code; no private detail is exposed.
func (e PublicError) Error() string { return e.Code }

func failure() PublicError { return PublicError{Code: FailureCode} }

// Sanitize reduces the internal error graph rooted at root to a single
// PublicError.
//
// The graph is traversed iteratively with a visited set, so cause cycles
// and shared subgraphs cannot cause unbounded work or stack growth.
// Selection depends only on the set of registered codes present — never
// on private messages, parameter payloads, or traversal order: the
// highest-priority code wins, ties break to the smaller code. Parameters
// are then whitelisted and strictly type-checked per the registry; for
// each declared parameter the first valid value in canonical
// breadth-first order wins. Any budget violation or absence of a
// registered code yields the unified failure.
func (r *Registry) Sanitize(root *Node, budget Budget) PublicError {
	if r == nil || root == nil {
		return failure()
	}
	budget = budget.withDefaults()

	type item struct {
		node  *Node
		depth int
	}
	visited := make(map[*Node]struct{}, 64)
	visited[root] = struct{}{}
	queue := []item{{node: root}}
	order := make([]*Node, 0, 64)

	bestCode := ""
	bestPriority := 0
	found := false

	for head := 0; head < len(queue); head++ {
		if len(order) >= budget.MaxNodes {
			return failure()
		}
		it := queue[head]
		if it.depth > budget.MaxDepth {
			return failure()
		}
		order = append(order, it.node)

		if spec, ok := r.specs[it.node.code]; ok {
			if !found ||
				spec.Priority > bestPriority ||
				(spec.Priority == bestPriority && it.node.code < bestCode) {
				found = true
				bestCode = it.node.code
				bestPriority = spec.Priority
			}
		}
		for _, cause := range it.node.causes {
			if cause == nil {
				continue
			}
			if _, seen := visited[cause]; seen {
				continue
			}
			visited[cause] = struct{}{}
			queue = append(queue, item{node: cause, depth: it.depth + 1})
		}
	}

	if !found {
		return failure()
	}

	spec := r.specs[bestCode]
	var params map[string]any
	remaining := len(spec.Params)
	for _, node := range order {
		if remaining == 0 {
			break
		}
		if node.code != bestCode {
			continue
		}
		for _, ps := range spec.Params {
			if params != nil {
				if _, done := params[ps.Name]; done {
					continue
				}
			}
			raw, ok := node.params[ps.Name]
			if !ok {
				continue
			}
			v, ok := validateParam(ps, raw)
			if !ok {
				continue
			}
			if params == nil {
				params = make(map[string]any, len(spec.Params))
			}
			params[ps.Name] = v
			remaining--
		}
	}
	return PublicError{Code: bestCode, Params: params}
}
