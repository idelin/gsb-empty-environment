package errser

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
)

// Limits bound the work encoding and decoding may do. Zero values are
// replaced by defaults; see withDefaults.
type Limits struct {
	MaxDepth      int // max cause/child nesting before truncation
	MaxNodes      int // max real nodes emitted / accepted
	MaxMsgLen     int // max runes per message before truncation
	MaxCtxKeys    int // max whitelisted context keys per node
	MaxInputBytes int // decode only: reject larger inputs before parsing
}

func (l Limits) withDefaults() Limits {
	if l.MaxDepth <= 0 {
		l.MaxDepth = 64
	}
	if l.MaxNodes <= 0 {
		l.MaxNodes = 1024
	}
	if l.MaxMsgLen <= 0 {
		l.MaxMsgLen = 512
	}
	if l.MaxCtxKeys <= 0 {
		l.MaxCtxKeys = 16
	}
	if l.MaxInputBytes <= 0 {
		l.MaxInputBytes = 1 << 20
	}
	return l
}

// ErrBudget is returned (in Strict mode) when a budget is exceeded.
var ErrBudget = errors.New("errser: budget exceeded")

// Envelope is the serialized form: a flat node list with integer edges.
// Identity is preserved because shared sub-graphs are emitted once and
// later occurrences become back-references (Ref).
type Envelope struct {
	V     int    `json:"v"`
	Root  int    `json:"root"`
	Nodes []Node `json:"nodes"`
}

// Node is one vertex of the error graph. Exactly one of three shapes:
//   - real node: Kind set, optional Cause/Children edges
//   - reference: Ref set (Cycle=true iff the target is an ancestor,
//     i.e. following it would loop)
//   - truncation marker: Kind=="truncated", Trunc holds the reason
type Node struct {
	ID       int            `json:"id"`
	Kind     string         `json:"kind,omitempty"`
	Msg      string         `json:"msg,omitempty"`
	Ctx      map[string]any `json:"ctx,omitempty"`
	Cause    *int           `json:"cause,omitempty"`
	Children []int          `json:"children,omitempty"`
	Ref      *int           `json:"ref,omitempty"`
	Cycle    bool           `json:"cycle,omitempty"`
	Trunc    string         `json:"trunc,omitempty"`
}

// Encoder serializes error graphs. Allow is the context-key whitelist:
// only these keys are emitted, so the public output is independent of
// any sensitive value carried by the input (declared non-leakage rule:
// two inputs differing only in non-whitelisted values serialize to
// byte-identical output, including all counts).
type Encoder struct {
	Limits Limits
	Allow  map[string]bool
	// Strict turns budget exhaustion into ErrBudget instead of
	// emitting a truncation marker.
	Strict bool
}

// Marshal is a convenience wrapper with default limits.
func Marshal(root error, allow map[string]bool) ([]byte, error) {
	return Encoder{Allow: allow}.Encode(root)
}

type encoder struct {
	lim    Limits
	allow  map[string]bool
	strict bool

	nodes  []Node
	ids    map[error]int  // assigned id for memoizable (pointer) nodes
	active map[error]bool // nodes on the current DFS stack (cycle detection)
	used   int            // real nodes emitted
	err    error
}

// Encode serializes root into a versioned envelope.
func (enc Encoder) Encode(root error) ([]byte, error) {
	e := &encoder{
		lim:    enc.Limits.withDefaults(),
		allow:  enc.Allow,
		strict: enc.Strict,
		ids:    map[error]int{},
		active: map[error]bool{},
	}
	rootID := e.encodeNode(root, 0)
	if e.err != nil {
		return nil, e.err
	}
	env := Envelope{V: 1, Root: rootID, Nodes: e.nodes}
	return json.Marshal(env)
}

func (e *encoder) fail(err error) int {
	if e.err == nil {
		e.err = err
	}
	return -1
}

// appendNode appends n and returns its id. Ref/trunc marker nodes are
// cheap and not counted against MaxNodes; real nodes are.
func (e *encoder) appendNode(n Node, real bool) int {
	if real {
		e.used++
	}
	n.ID = len(e.nodes)
	e.nodes = append(e.nodes, n)
	return n.ID
}

func (e *encoder) trunc(reason string) int {
	if e.strict {
		return e.fail(fmt.Errorf("%w: %s", ErrBudget, reason))
	}
	return e.appendNode(Node{Kind: string(KindTruncated), Trunc: reason}, false)
}

// memoizable reports whether identity tracking is safe for err's dynamic
// type (interface map keys must be comparable; our pointer types are).
func memoizable(err error) bool {
	switch err.(type) {
	case *Error, *Aggregate:
		return true
	}
	return false
}

func (e *encoder) encodeNode(err error, depth int) int {
	if e.err != nil {
		return -1
	}
	if err == nil {
		return e.appendNode(Node{Kind: string(KindOpaque), Msg: "<nil>"}, false)
	}
	if memoizable(err) {
		if id, ok := e.ids[err]; ok {
			ref := id
			return e.appendNode(Node{Ref: &ref, Cycle: e.active[err]}, false)
		}
	}
	if depth > e.lim.MaxDepth {
		return e.trunc("depth")
	}
	if e.used >= e.lim.MaxNodes {
		return e.trunc("nodes")
	}

	switch t := err.(type) {
	case *Error:
		if t.kind == KindTruncated {
			// Previously decoded truncation marker: round-trips as-is.
			return e.appendNode(Node{Kind: string(KindTruncated), Trunc: t.msg}, true)
		}
		id := e.appendNode(Node{}, true)
		e.ids[err] = id
		e.active[err] = true
		n := Node{
			Kind: string(t.kind),
			Msg:  truncMsg(t.msg, e.lim.MaxMsgLen),
			Ctx:  e.sanitizeCtx(t.ctx),
		}
		if t.cause != nil {
			c := e.encodeNode(t.cause, depth+1)
			if e.err != nil {
				return -1
			}
			n.Cause = &c
		}
		e.nodes[id] = n
		e.nodes[id].ID = id
		delete(e.active, err)
		return id

	case *Aggregate:
		id := e.appendNode(Node{}, true)
		e.ids[err] = id
		e.active[err] = true
		n := Node{
			Kind: string(KindAggregate),
			Msg:  truncMsg(t.msg, e.lim.MaxMsgLen),
		}
		for _, c := range t.errs {
			cid := e.encodeNode(c, depth+1)
			if e.err != nil {
				return -1
			}
			n.Children = append(n.Children, cid)
		}
		e.nodes[id] = n
		e.nodes[id].ID = id
		delete(e.active, err)
		return id

	default:
		// Foreign error: generic code, no kind-specific handling.
		// err.Error() is the caller's own display text; we truncate it.
		n := Node{Kind: string(KindOpaque), Msg: truncMsg(t.Error(), e.lim.MaxMsgLen)}
		id := e.appendNode(n, true)
		// Follow a single-cause Unwrap within the depth budget.
		if cause := errors.Unwrap(err); cause != nil {
			c := e.encodeNode(cause, depth+1)
			if e.err != nil {
				return -1
			}
			e.nodes[id].Cause = &c
		}
		return id
	}
}

// sanitizeCtx keeps only whitelisted keys and safe scalar values.
// Non-whitelisted keys are dropped entirely: neither their values nor
// their presence is observable in the output.
func (e *encoder) sanitizeCtx(in map[string]any) map[string]any {
	if len(in) == 0 {
		return nil
	}
	keys := make([]string, 0, len(in))
	for k := range in {
		keys = append(keys, k)
	}
	sort.Strings(keys) // deterministic output independent of map order
	var out map[string]any
	for _, k := range keys {
		if !e.allow[k] {
			continue
		}
		if len(out) >= e.lim.MaxCtxKeys {
			break
		}
		if out == nil {
			out = map[string]any{}
		}
		out[k] = safeValue(in[k], e.lim.MaxMsgLen)
	}
	return out
}

// safeValue passes scalars through and replaces everything else with a
// type-name placeholder. It never calls String() or any other method on
// the value, so encoding executes no host code.
func safeValue(v any, maxLen int) any {
	switch t := v.(type) {
	case nil, bool:
		return t
	case string:
		return truncMsg(t, maxLen)
	case int, int8, int16, int32, int64,
		uint, uint8, uint16, uint32, uint64,
		float32, float64:
		return t
	default:
		return fmt.Sprintf("<%T>", v)
	}
}

// truncMsg caps a message at max runes, appending an ellipsis marker.
func truncMsg(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max]) + "…"
}
