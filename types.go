package netsec

import (
	"errors"
	"strings"
)

// Address is the final host address a symbol resolves to once the full
// forwarding chain has been followed. Address 0 is never valid.
type Address uint64

// ModuleID identifies a module by name and content digest. Two modules with
// the same name but different digests are distinct identities, so a binding
// that targets one digest can never be satisfied by another.
type ModuleID struct {
	Name   string
	Digest string
}

// String renders an identity as name@digest.
func (m ModuleID) String() string {
	return m.Name + "@" + m.Digest
}

// Ref is a symbol inside a specific module identity.
type Ref struct {
	Module ModuleID
	Symbol string
}

// String renders a reference as name@digest#symbol.
func (r Ref) String() string {
	return r.Module.String() + "#" + r.Symbol
}

// Binding is what a symbol name means inside a module.
//
// A binding is either terminal (Concrete is true and Addr is non-zero) or a
// forwarding entry (Concrete is false and To points at another module and
// symbol). A forwarding entry can itself be forwarded, so resolution must walk
// the whole chain instead of trusting the first hop.
type Binding struct {
	Concrete bool
	Addr     Address
	To       Ref
}

// Resolved records one fully resolved symbol: its final host address and every
// module identity traversed to reach it. The first element of Trail is the
// starting reference's module and the last element is the module that owns the
// concrete address.
type Resolved struct {
	Addr  Address
	Trail []ModuleID
}

// Sentinel errors. Callers use errors.Is to classify failures.
var (
	// ErrUnknownModule means a referenced module identity is not present in the
	// version graph.
	ErrUnknownModule = errors.New("netsec: unknown module")
	// ErrUnauthorized means a module on the resolution path is not authorized.
	ErrUnauthorized = errors.New("netsec: unauthorized module")
	// ErrCycle means the forwarding graph contains a reachable cycle.
	ErrCycle = errors.New("netsec: forwarding cycle")
	// ErrUnknownSymbol means no binding exists for the symbol.
	ErrUnknownSymbol = errors.New("netsec: unknown symbol")
	// ErrInvalidAddress means a concrete binding uses address zero.
	ErrInvalidAddress = errors.New("netsec: invalid address")
	// ErrInvalidRef means a module identity or reference is malformed.
	ErrInvalidRef = errors.New("netsec: invalid module or symbol reference")
	// ErrModuleExists means an identity is re-added with conflicting data.
	ErrModuleExists = errors.New("netsec: module already exists")
	// ErrTxClosed means a transaction was used after commit or discard.
	ErrTxClosed = errors.New("netsec: transaction closed")
)

// Error attaches module-identity context and the full hop trail to a
// resolution or transaction failure.
type Error struct {
	Op    string
	Ref   Ref
	Trail []ModuleID
	Err   error
}

func (e *Error) Error() string {
	var b strings.Builder
	b.WriteString("netsec: ")
	if e.Op != "" {
		b.WriteString(e.Op)
		b.WriteString(": ")
	}
	b.WriteString(e.Err.Error())
	if e.Ref.Symbol != "" || e.Ref.Module.Name != "" {
		b.WriteString(" at ")
		b.WriteString(e.Ref.String())
	}
	if len(e.Trail) > 0 {
		b.WriteString(" via ")
		for i, m := range e.Trail {
			if i > 0 {
				b.WriteString(" -> ")
			}
			b.WriteString(m.String())
		}
	}
	return b.String()
}

// Unwrap exposes the wrapped sentinel for errors.Is / errors.As.
func (e *Error) Unwrap() error { return e.Err }

func (e *Error) withTrail(trail []ModuleID) *Error {
	cp := make([]ModuleID, len(trail))
	copy(cp, trail)
	return &Error{Op: e.Op, Ref: e.Ref, Trail: cp, Err: e.Err}
}

func fail(op string, ref Ref, err error) *Error {
	return &Error{Op: op, Ref: ref, Err: err}
}

func validModule(m ModuleID) bool {
	return m.Name != "" && m.Digest != ""
}

func (r Ref) valid() bool {
	return validModule(r.Module) && r.Symbol != ""
}

func (b Binding) validate() error {
	switch {
	case b.Concrete:
		if b.Addr == 0 {
			return ErrInvalidAddress
		}
	default:
		if !b.To.valid() {
			return ErrInvalidRef
		}
	}
	return nil
}
