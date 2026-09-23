// Package errser serializes error chains and aggregate errors into a
// safe, bounded, JSON-based graph document.
//
// Supported serialization scope (by design, nothing else is supported):
//   - Nodes carry a stable type code, a human display message, and a
//     context map whose values are reduced to short scalars.
//   - Edges are a single "cause" link and an ordered "children" list
//     (aggregate errors). Shared sub-errors are serialized once and
//     referenced by identity; cycles are representable and terminate.
//   - Context keys are emitted only when present in an explicit
//     whitelist. Non-whitelisted keys contribute only to a per-node
//     redaction counter; neither their keys nor values are emitted.
//   - Decoding never executes host code, never uses reflection to
//     instantiate arbitrary types, and validates every declared length
//     before allocating. Unknown type codes decode to a generic code.
package errser

import (
	"fmt"
	"strings"
)

// GenericCode is the stable code assigned to any error whose concrete
// type or serialized code is not registered. Unknown types never keep
// their original code across a decode boundary.
const GenericCode = "err.unknown"

// AggregateCode is the stable code for aggregate (multi-child) errors.
const AggregateCode = "err.aggregate"

// externalCode is the stable code for plain non-errser errors that are
// wrapped into the graph at serialization time.
const externalCode = "err.external"

// registeredCodes is the explicit set of type codes that may survive a
// serialize/deserialize round trip. Anything else decodes as GenericCode.
var registeredCodes = map[string]bool{
	GenericCode:   true,
	AggregateCode: true,
	externalCode:  true,
}

// RegisterCode adds a stable application-specific type code to the
// registry. Codes are stable identifiers only; display text lives in
// each node's message and is never derived from the code.
func RegisterCode(code string) {
	if code != "" {
		registeredCodes[code] = true
	}
}

// Error is a node in a serializable error graph. The zero value is not
// useful; construct with New or NewAggregate.
type Error struct {
	code     string
	msg      string
	ctx      map[string]any
	cause    *Error
	children []*Error
}

// New creates an error node with a stable type code and display message.
func New(code, msg string) *Error {
	return &Error{code: code, msg: msg}
}

// NewAggregate creates an aggregate error node holding child errors.
func NewAggregate(code, msg string, children ...*Error) *Error {
	if code == "" {
		code = AggregateCode
	}
	e := &Error{code: code, msg: msg}
	e.children = append(e.children, children...)
	return e
}

// WithContext attaches a context field. Only whitelisted keys are ever
// serialized; values are reduced to bounded scalars at encode time.
func (e *Error) WithContext(key string, value any) *Error {
	if e.ctx == nil {
		e.ctx = make(map[string]any)
	}
	e.ctx[key] = value
	return e
}

// SetCause sets the cause edge. Cycles are permitted; all traversals
// performed by this package are visit-guarded and terminate.
func (e *Error) SetCause(cause *Error) *Error {
	e.cause = cause
	return e
}

// Add appends a child to an aggregate node.
func (e *Error) Add(children ...*Error) *Error {
	e.children = append(e.children, children...)
	return e
}

// Code returns the stable type code.
func (e *Error) Code() string { return e.code }

// Message returns the human-readable display text.
func (e *Error) Message() string { return e.msg }

// Cause returns the cause edge, or nil.
func (e *Error) Cause() *Error { return e.cause }

// Children returns the aggregate children.
func (e *Error) Children() []*Error { return e.children }

// Error renders the graph as text. It is visit-guarded so cyclic and
// shared-subgraph structures terminate; repeated nodes are marked.
func (e *Error) Error() string {
	var b strings.Builder
	seen := make(map[*Error]bool)
	var walk func(n *Error, depth int)
	walk = func(n *Error, depth int) {
		if n == nil {
			return
		}
		if depth > 64 {
			b.WriteString("…")
			return
		}
		if seen[n] {
			fmt.Fprintf(&b, "%s (shared)", n.code)
			return
		}
		seen[n] = true
		fmt.Fprintf(&b, "%s: %s", n.code, n.msg)
		if n.cause != nil {
			b.WriteString(" <- ")
			walk(n.cause, depth+1)
		}
		for _, c := range n.children {
			b.WriteString(" [")
			walk(c, depth+1)
			b.WriteString("]")
		}
	}
	walk(e, 0)
	return b.String()
}
