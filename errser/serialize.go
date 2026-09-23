package errser

import (
	"encoding/json"
	"errors"
	"fmt"
)

// ErrBudgetExceeded is returned when a hard budget (node count or input
// byte size) is exceeded. Soft limits produce truncation markers instead.
var ErrBudgetExceeded = errors.New("errser: budget exceeded")

// Options bounds serialization and deserialization. All limits are
// mandatory; use DefaultOptions for sane values.
type Options struct {
	// MaxNodes is a hard budget: graphs with more distinct nodes fail
	// with ErrBudgetExceeded rather than being silently truncated.
	MaxNodes int
	// MaxDepth is a soft limit: edges deeper than this are replaced by
	// a truncation marker on the parent node.
	MaxDepth int
	// MaxMessageLen caps display text per node (truncation marker set).
	MaxMessageLen int
	// MaxContextKeys caps whitelisted context keys emitted per node.
	MaxContextKeys int
	// MaxContextValueLen caps the string form of each context value.
	MaxContextValueLen int
	// MaxBytes is a hard budget on serialized input size for Unmarshal.
	MaxBytes int
	// Whitelist is the set of context keys that may be emitted.
	Whitelist map[string]bool
}

// DefaultOptions returns the recommended bounded configuration.
func DefaultOptions() Options {
	return Options{
		MaxNodes:           1024,
		MaxDepth:           32,
		MaxMessageLen:      512,
		MaxContextKeys:     16,
		MaxContextValueLen: 128,
		MaxBytes:           1 << 20,
		Whitelist:          map[string]bool{},
	}
}

// AllowKeys adds keys to the whitelist and returns the options.
func (o Options) AllowKeys(keys ...string) Options {
	if o.Whitelist == nil {
		o.Whitelist = map[string]bool{}
	}
	for _, k := range keys {
		o.Whitelist[k] = true
	}
	return o
}

// Document is the serialized form: a flat node list with ID edges.
// Identity is preserved: shared sub-errors appear exactly once.
type Document struct {
	Version   int    `json:"version"`
	Root      int    `json:"root"`
	Truncated bool   `json:"truncated,omitempty"`
	Nodes     []Node `json:"nodes"`
}

// Node is one serialized error. Cause is -1 when absent. Children and
// Cause reference other nodes by ID, which is how shared causes and
// cycles are represented without recursion.
type Node struct {
	ID        int               `json:"id"`
	Code      string            `json:"code"`
	Message   string            `json:"message"`
	Context   map[string]string `json:"context,omitempty"`
	Redacted  int               `json:"redacted,omitempty"`
	Cause     int               `json:"cause"`
	Children  []int             `json:"children,omitempty"`
	Truncated bool              `json:"truncated,omitempty"`
}

const version = 1

// Marshal serializes an error graph into a bounded JSON document.
// Traversal is visit-guarded: shared causes are emitted once and cycles
// terminate. Non-*Error values reachable via the graph are converted to
// generic external nodes carrying only their display text.
func Marshal(root *Error, opts Options) ([]byte, error) {
	if root == nil {
		return nil, errors.New("errser: nil root")
	}
	enc := &encoder{opts: opts, ids: make(map[*Error]int)}
	rootID, err := enc.emit(root, 0)
	if err != nil {
		return nil, err
	}
	d := Document{Version: version, Root: rootID, Nodes: enc.nodes, Truncated: enc.truncated}
	return json.Marshal(d)
}

type encoder struct {
	opts      Options
	ids       map[*Error]int
	nodes     []Node
	truncated bool
}

func (enc *encoder) emit(e *Error, depth int) (int, error) {
	if id, ok := enc.ids[e]; ok {
		return id, nil // shared cause or cycle: reference existing identity
	}
	if len(enc.nodes) >= enc.opts.MaxNodes {
		return 0, ErrBudgetExceeded
	}
	id := len(enc.nodes)
	enc.ids[e] = id
	node := Node{ID: id, Code: e.code, Cause: -1}
	enc.nodes = append(enc.nodes, node) // reserve slot so cycles resolve

	node.Message, node.Truncated = truncate(e.msg, enc.opts.MaxMessageLen)
	node.Context, node.Redacted, node.Truncated = enc.filterContext(e.ctx, node.Truncated)

	if depth >= enc.opts.MaxDepth {
		if e.cause != nil || len(e.children) > 0 {
			node.Truncated = true
			enc.truncated = true
		}
	} else {
		if e.cause != nil {
			cid, err := enc.emit(e.cause, depth+1)
			if err != nil {
				return 0, err
			}
			node.Cause = cid
		}
		for _, c := range e.children {
			if c == nil {
				continue
			}
			cid, err := enc.emit(c, depth+1)
			if err != nil {
				return 0, err
			}
			node.Children = append(node.Children, cid)
		}
	}
	if node.Truncated {
		enc.truncated = true
	}
	enc.nodes[id] = node
	return id, nil
}

// filterContext emits only whitelisted keys with bounded scalar values.
// Non-whitelisted fields contribute only to the redaction counter, so
// two inputs differing only in sensitive values produce identical
// public structure and counts.
func (enc *encoder) filterContext(ctx map[string]any, truncated bool) (map[string]string, int, bool) {
	var out map[string]string
	redacted := 0
	keys := make([]string, 0, len(ctx))
	for k := range ctx {
		keys = append(keys, k)
	}
	sortStrings(keys)
	for _, k := range keys {
		if !enc.opts.Whitelist[k] {
			redacted++
			continue
		}
		if len(out) >= enc.opts.MaxContextKeys {
			truncated = true
			continue
		}
		if out == nil {
			out = make(map[string]string)
		}
		v, t := truncate(scalarString(ctx[k]), enc.opts.MaxContextValueLen)
		out[k] = v
		truncated = truncated || t
	}
	return out, redacted, truncated
}

// scalarString reduces a context value to a bounded scalar string.
// Only scalar kinds are rendered; anything else becomes a placeholder
// so no host internals leak through formatting.
func scalarString(v any) string {
	switch x := v.(type) {
	case nil:
		return "null"
	case string:
		return x
	case bool:
		return fmt.Sprintf("%t", x)
	case int, int8, int16, int32, int64,
		uint, uint8, uint16, uint32, uint64,
		float32, float64:
		return fmt.Sprintf("%v", x)
	default:
		return "[unsupported]"
	}
}

func truncate(s string, max int) (string, bool) {
	if max <= 0 || len(s) <= max {
		return s, false
	}
	if max <= 1 {
		return s[:max], true
	}
	return s[:max-1] + "…", true
}

func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}
