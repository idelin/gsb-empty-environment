// Package resolver resolves name references against parsed declarations.
//
// Declarations are located by (module, name). A declaration is either a
// real definition, a re-export of another symbol (which may form chains),
// or an import of a symbol from another module. A local declaration
// (definition or re-export) shadows an import of the same name in the
// same module. Re-export chains that form a cycle without ever reaching
// a real definition are reported as cyclic-unresolved.
package resolver

// Key locates a declaration by module and name.
type Key struct {
	Module string
	Name   string
}

// Kind classifies a declaration.
type Kind int

const (
	// Definition is a real definition (function, type, variable, ...).
	Definition Kind = iota
	// Reexport re-exports Target under this module and name.
	Reexport
	// Import brings Target into scope under this module and name.
	Import
)

func (k Kind) String() string {
	switch k {
	case Definition:
		return "definition"
	case Reexport:
		return "reexport"
	case Import:
		return "import"
	}
	return "unknown"
}

// Decl is a parsed declaration. TargetModule/TargetName are only
// meaningful for Reexport and Import declarations.
type Decl struct {
	Module       string
	Name         string
	Kind         Kind
	TargetModule string
	TargetName   string
}

// Key returns the location of the declaration itself.
func (d Decl) Key() Key { return Key{Module: d.Module, Name: d.Name} }

// Target returns the location the declaration points to.
func (d Decl) Target() Key { return Key{Module: d.TargetModule, Name: d.TargetName} }

// Ref is a parsed reference: a use of Name inside Module.
type Ref struct {
	ID     int
	Module string
	Name   string
}

// Step is one hop in the explanation chain of a resolution.
type Step struct {
	Key    Key
	Kind   Kind
	Detail string
}

// Result is the outcome of resolving one reference.
type Result struct {
	Ref      Ref
	Resolved bool   // a real definition was reached
	Cyclic   bool   // resolution looped without a real definition
	Endpoint Key    // valid only when Resolved is true
	Chain    []Step // explanation chain, in resolution order
	Reason   string
}
