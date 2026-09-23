// Package errser serializes error chains and aggregate errors into a
// versioned, language-neutral envelope. Stable type codes (Kind) are kept
// separate from human display text (message). Context fields are emitted
// only through an explicit whitelist so sensitive values never leak.
//
// Supported serialization scope (declared contract):
//   - *Error nodes: stable Kind, message, whitelisted scalar context, one cause.
//   - *Aggregate nodes: message plus an ordered list of child errors.
//   - Foreign error types: encoded as KindOpaque leaves; their Unwrap chain
//     is followed only up to the depth budget.
//   - Context values: only nil, bool, string and numeric scalars survive;
//     anything else is replaced by a "<pkg.Type>" placeholder (never by
//     calling String(), so encoding executes no host code).
//
// Decoding guarantees:
//   - Input length is validated before any allocation proportional to it.
//   - Node counts and string lengths are validated before the graph is built.
//   - Kinds dispatch through a closed switch; unknown kinds become KindOpaque.
//     Decoding never uses reflection and never instantiates host types named
//     by the payload, so it cannot execute host code.
//   - Cyclic and shared sub-graphs are reconstructed with identity preserved;
//     Error() and re-serialization of cyclic graphs terminate.
package errser

import (
	"fmt"
	"strings"
)

// Kind is a stable, machine-readable type code. It is deliberately
// decoupled from the human-readable message so display text can change
// (or be localized) without breaking programmatic handling.
type Kind string

const (
	KindValidation Kind = "validation"
	KindTimeout    Kind = "timeout"
	KindAuth       Kind = "auth"
	KindInternal   Kind = "internal"
	KindAggregate  Kind = "aggregate"
	// KindTruncated marks a node whose subtree was cut by a budget.
	KindTruncated Kind = "truncated"
	// KindOpaque is the generic code kept for any unknown error type,
	// both when encoding foreign errors and when decoding unknown kinds.
	KindOpaque Kind = "opaque"
)

// maxPrintDepth bounds Error() rendering so cyclic graphs terminate.
const maxPrintDepth = 32

// Error is a single error node with a stable kind, display message,
// safe scalar context and at most one cause.
type Error struct {
	kind  Kind
	msg   string
	ctx   map[string]any
	cause error
}

// New creates an Error with the given stable kind and display message.
func New(kind Kind, msg string) *Error {
	return &Error{kind: kind, msg: msg}
}

// With attaches a context key/value. Only whitelisted keys with scalar
// values are ever serialized; see Encoder.
func (e *Error) With(key string, val any) *Error {
	if e.ctx == nil {
		e.ctx = map[string]any{}
	}
	e.ctx[key] = val
	return e
}

// Wrap sets the cause, forming a chain. Cycles are permitted; Error()
// and serialization both terminate on them.
func (e *Error) Wrap(cause error) *Error {
	e.cause = cause
	return e
}

// Kind returns the stable type code.
func (e *Error) Kind() Kind { return e.kind }

// Context returns a copy of the raw (unfiltered) context.
func (e *Error) Context() map[string]any {
	out := make(map[string]any, len(e.ctx))
	for k, v := range e.ctx {
		out[k] = v
	}
	return out
}

// Unwrap exposes the cause for errors.Is/As.
func (e *Error) Unwrap() error { return e.cause }

// Is matches by stable kind, so identity survives serialization:
// a decoded *Error Is-equal to a sentinel of the same kind.
func (e *Error) Is(target error) bool {
	t, ok := target.(*Error)
	return ok && t.kind == e.kind
}

func (e *Error) Error() string {
	var b strings.Builder
	printErr(&b, e, 0)
	return b.String()
}

// Aggregate is an error that bundles several child errors.
type Aggregate struct {
	msg  string
	errs []error
}

// NewAggregate creates an aggregate error node.
func NewAggregate(msg string, errs ...error) *Aggregate {
	return &Aggregate{msg: msg, errs: errs}
}

// Errors returns the child errors.
func (a *Aggregate) Errors() []error { return append([]error(nil), a.errs...) }

// Unwrap exposes children for errors.Is/As (Go 1.20 multi-unwrap).
func (a *Aggregate) Unwrap() []error { return a.errs }

func (a *Aggregate) Error() string {
	var b strings.Builder
	printErr(&b, a, 0)
	return b.String()
}

// printErr renders with a depth cap so cyclic graphs cannot recurse forever.
func printErr(b *strings.Builder, err error, depth int) {
	if err == nil {
		return
	}
	if depth >= maxPrintDepth {
		b.WriteString(" …")
		return
	}
	switch t := err.(type) {
	case *Error:
		if t.kind == KindTruncated {
			fmt.Fprintf(b, "[truncated: %s]", t.msg)
			return
		}
		fmt.Fprintf(b, "[%s] %s", t.kind, t.msg)
		if t.cause != nil {
			b.WriteString(": ")
			printErr(b, t.cause, depth+1)
		}
	case *Aggregate:
		fmt.Fprintf(b, "%s (%d error(s))", t.msg, len(t.errs))
		for i, c := range t.errs {
			fmt.Fprintf(b, "\n  %d: ", i)
			printErr(b, c, depth+1)
		}
	default:
		// Foreign errors are outside the supported scope; we print them
		// but do not traverse them here.
		b.WriteString(t.Error())
	}
}
