// Package resolve performs name resolution over already-parsed
// declarations and references.
//
// Declarations are located by (module, name). A declaration is either a
// real definition or a re-export pointing at another (module, name), so
// re-exports may form chains. A module may also import a name from
// another module; a local declaration with the same name shadows the
// import. Resolving a reference walks the chain until it reaches a real
// definition (resolved), runs out of declarations (unresolved), or
// revisits a key without ever hitting a definition (cycle unresolved).
package resolve

import (
	"fmt"
	"strings"
)

// Key locates a declaration or reference by module and name.
type Key struct {
	Module string
	Name   string
}

func (k Key) String() string { return k.Module + "." + k.Name }

// Kind is the kind of a declaration.
type Kind int

const (
	// Def is a real definition; it terminates a resolution chain.
	Def Kind = iota
	// ReExport forwards resolution to Target.
	ReExport
)

// Decl is a parsed declaration. Target is only used when Kind == ReExport.
type Decl struct {
	Kind   Kind
	Target Key
}

// Status is the outcome of resolving a reference.
type Status int

const (
	// Resolved: the chain ended at a real definition (Endpoint).
	Resolved Status = iota
	// Unresolved: the chain ended at a key with no declaration and no import.
	Unresolved
	// CycleUnresolved: the chain loops without ever reaching a definition.
	CycleUnresolved
)

func (s Status) String() string {
	switch s {
	case Resolved:
		return "resolved"
	case Unresolved:
		return "unresolved"
	case CycleUnresolved:
		return "cycle unresolved"
	}
	return "unknown"
}

// Step is one hop of a resolution explanation chain.
type Step struct {
	Key  Key
	Via  string // "definition", "re-export" or "import"
	Note string // e.g. that a local declaration shadows an import
}

// Result is the resolution of one reference: its endpoint plus the
// explanation chain that led there.
type Result struct {
	Status   Status
	Endpoint Key    // the real definition; valid when Status == Resolved
	Chain    []Step // every hop taken, in order
	Cycle    []Key  // the looping keys; valid when Status == CycleUnresolved
}

// Explain renders the resolution as a human-readable chain.
func (r Result) Explain() string {
	var b strings.Builder
	for i, s := range r.Chain {
		if i > 0 {
			b.WriteString(" -> ")
		}
		fmt.Fprintf(&b, "%s (%s)", s.Key, s.Via)
		if s.Note != "" {
			fmt.Fprintf(&b, " [%s]", s.Note)
		}
	}
	switch r.Status {
	case Resolved:
		fmt.Fprintf(&b, " => resolved at %s", r.Endpoint)
	case Unresolved:
		if len(r.Chain) > 0 {
			b.WriteString(" -> ")
		}
		b.WriteString("? => unresolved")
	case CycleUnresolved:
		fmt.Fprintf(&b, " => cycle unresolved: %s", joinKeys(r.Cycle))
	}
	return b.String()
}

func joinKeys(keys []Key) string {
	parts := make([]string, len(keys))
	for i, k := range keys {
		parts[i] = k.String()
	}
	return strings.Join(parts, " -> ")
}

// store holds the raw declaration/import tables shared by the direct
// model and the incremental resolver.
type store struct {
	locals  map[Key]Decl // local declarations (Def or ReExport)
	imports map[Key]Key  // (module, name) -> imported (module, name)
}

func newStore() store {
	return store{locals: make(map[Key]Decl), imports: make(map[Key]Key)}
}

// SetLocal adds or replaces the local declaration at key.
func (s *store) SetLocal(key Key, d Decl) { s.locals[key] = d }

// DeleteLocal removes the local declaration at key, if any.
func (s *store) DeleteLocal(key Key) { delete(s.locals, key) }

// SetImport makes (from.Module, from.Name) import `to`.
func (s *store) SetImport(from, to Key) { s.imports[from] = to }

// DeleteImport removes the import at from, if any.
func (s *store) DeleteImport(from Key) { delete(s.imports, from) }

// walk resolves start against the given tables. visit, if non-nil, is
// called with every key the resolution depends on (including keys with
// no declaration, since adding one would change the outcome).
func walk(locals map[Key]Decl, imports map[Key]Key, start Key, visit func(Key)) Result {
	var chain []Step
	seen := make(map[Key]int) // key -> index of its step in chain
	cur := start
	for {
		if visit != nil {
			visit(cur)
		}
		if idx, ok := seen[cur]; ok {
			cycle := make([]Key, 0, len(chain)-idx)
			for _, st := range chain[idx:] {
				cycle = append(cycle, st.Key)
			}
			return Result{Status: CycleUnresolved, Chain: chain, Cycle: cycle}
		}
		seen[cur] = len(chain)

		if d, ok := locals[cur]; ok {
			step := Step{Key: cur}
			if tgt, imported := imports[cur]; imported {
				step.Note = "local declaration shadows import of " + tgt.String()
			}
			if d.Kind == Def {
				step.Via = "definition"
				chain = append(chain, step)
				return Result{Status: Resolved, Endpoint: cur, Chain: chain}
			}
			step.Via = "re-export"
			chain = append(chain, step)
			cur = d.Target
			continue
		}
		if tgt, ok := imports[cur]; ok {
			chain = append(chain, Step{Key: cur, Via: "import"})
			cur = tgt
			continue
		}
		return Result{Status: Unresolved, Chain: chain}
	}
}

// Direct is the direct resolution model: it resolves every reference
// from scratch against the current tables. It is the ground truth used
// to cross-check the incremental Resolver.
type Direct struct {
	store
}

// NewDirect returns an empty direct model.
func NewDirect() *Direct { return &Direct{store: newStore()} }

// Resolve resolves the reference (module, name) from scratch.
func (d *Direct) Resolve(start Key) Result {
	return walk(d.locals, d.imports, start, nil)
}
