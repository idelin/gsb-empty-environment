package attrgram

import (
	"fmt"
	"sort"
	"strings"
)

// AttrID identifies one attribute instance: attribute Name attached to an
// AST Node (e.g. Binary@3:5.typ).
type AttrID struct {
	Node Node
	Name string
}

func (a AttrID) String() string {
	return fmt.Sprintf("%s@%s.%s", nodeName(a.Node), a.Node.Position(), a.Name)
}

// EvalFunc computes an attribute value. get resolves the value of a
// dependency, which is guaranteed to be evaluated before fn runs.
type EvalFunc func(get func(AttrID) interface{}) interface{}

type instance struct {
	id   AttrID
	deps []AttrID
	fn   EvalFunc
}

// Engine is the syntax-directed translation kernel. Attribute instances are
// registered together with their dependencies; Eval topologically sorts the
// dependency graph (Kahn's algorithm) and evaluates attributes in an order
// where every dependency is computed before its dependents.
type Engine struct {
	order  []*instance
	byID   map[AttrID]*instance
	Values map[AttrID]interface{}
}

func NewEngine() *Engine {
	return &Engine{
		byID:   make(map[AttrID]*instance),
		Values: make(map[AttrID]interface{}),
	}
}

// Add registers an attribute instance. deps must reference attributes that
// are (or will be) registered. Registering the same id twice panics.
func (e *Engine) Add(id AttrID, deps []AttrID, fn EvalFunc) {
	if _, dup := e.byID[id]; dup {
		panic(fmt.Sprintf("attrgram: duplicate attribute %s", id))
	}
	seen := make(map[AttrID]bool)
	var uniq []AttrID
	for _, d := range deps {
		if d != id && !seen[d] {
			seen[d] = true
			uniq = append(uniq, d)
		}
	}
	inst := &instance{id: id, deps: uniq, fn: fn}
	e.byID[id] = inst
	e.order = append(e.order, inst)
}

// CycleError is returned by Eval when the attribute dependency graph
// contains a cycle. Remaining lists the attribute instances that could not
// be scheduled.
type CycleError struct {
	Remaining []AttrID
}

func (c *CycleError) Error() string {
	names := make([]string, len(c.Remaining))
	for i, id := range c.Remaining {
		names[i] = id.String()
	}
	sort.Strings(names)
	return fmt.Sprintf("attrgram: cyclic attribute dependency involving: %s",
		strings.Join(names, ", "))
}

// Eval evaluates all registered attributes in topological dependency order.
// Evaluation order is deterministic (registration order among ready nodes).
// It returns a *CycleError if the dependency graph is cyclic.
func (e *Engine) Eval() error {
	indeg := make(map[*instance]int, len(e.order))
	dependents := make(map[*instance][]*instance)
	for _, in := range e.order {
		for _, d := range in.deps {
			dep, ok := e.byID[d]
			if !ok {
				return fmt.Errorf("attrgram: attribute %s depends on undefined attribute %s", in.id, d)
			}
			indeg[in]++
			dependents[dep] = append(dependents[dep], in)
		}
	}
	var queue []*instance
	for _, in := range e.order {
		if indeg[in] == 0 {
			queue = append(queue, in)
		}
	}
	processed := 0
	get := func(id AttrID) interface{} { return e.Values[id] }
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		e.Values[cur.id] = cur.fn(get)
		processed++
		for _, next := range dependents[cur] {
			indeg[next]--
			if indeg[next] == 0 {
				queue = append(queue, next)
			}
		}
	}
	if processed < len(e.order) {
		var remaining []AttrID
		for _, in := range e.order {
			if indeg[in] > 0 {
				remaining = append(remaining, in.id)
			}
		}
		return &CycleError{Remaining: remaining}
	}
	return nil
}
