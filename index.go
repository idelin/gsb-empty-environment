package netsec

import (
	"errors"
	"fmt"
)

// ObjectID identifies an abstract object. IDs 0 and 1 are reserved for the
// shared prototype and the fixed constructor object.
type ObjectID int

const (
	SharedPrototypeID ObjectID = iota
	ConstructorID
	firstAllocationID
)

// ObjectKind classifies an ObjectID.
type ObjectKind string

const (
	KindSharedPrototype ObjectKind = "shared-prototype"
	KindConstructor     ObjectKind = "constructor"
	KindAllocated       ObjectKind = "allocated"
)

// ObjectRef is a stable reference to one of the analysis objects.
type ObjectRef struct {
	ID     ObjectID
	Kind   ObjectKind
	Label  string
	NodeID int
}

// MaxObjects is the maximum number of allocation sites a program may contain.
const MaxObjects = 32

// MaxLoopIterations is the maximum supported loop bound.
const MaxLoopIterations = 16

// programIndex assigns deterministic IDs to syntax nodes and allocation
// sites. IDs are used in findings and concrete-execution comparison.
type programIndex struct {
	nodeIDs map[Node]int
	objects map[*NewExpr]ObjectID
	sites   []*NewExpr
}

func indexProgram(p *Program) *programIndex {
	ix := &programIndex{
		nodeIDs: make(map[Node]int),
		objects: make(map[*NewExpr]ObjectID),
	}
	if p == nil {
		return ix
	}
	for _, s := range p.Stmts {
		ix.indexStmt(s)
	}
	return ix
}

func (ix *programIndex) nextID(n Node) int {
	if id, ok := ix.nodeIDs[n]; ok {
		return id
	}
	id := len(ix.nodeIDs) + 1
	ix.nodeIDs[n] = id
	return id
}

func (ix *programIndex) indexStmt(s Stmt) {
	if s == nil {
		return
	}
	switch x := s.(type) {
	case *AssignStmt:
		ix.nextID(x)
		ix.indexExpr(x.Target)
		ix.indexExpr(x.Value)
	case *ExprStmt:
		ix.nextID(x)
		ix.indexExpr(x.X)
	case *IfStmt:
		ix.nextID(x)
		ix.indexExpr(x.Cond)
		for _, inner := range x.Then {
			ix.indexStmt(inner)
		}
		for _, inner := range x.Else {
			ix.indexStmt(inner)
		}
	case *WhileStmt:
		ix.nextID(x)
		ix.indexExpr(x.Cond)
		for _, inner := range x.Body {
			ix.indexStmt(inner)
		}
	case *RepeatStmt:
		ix.nextID(x)
		for _, inner := range x.Body {
			ix.indexStmt(inner)
		}
	}
}

func (ix *programIndex) indexExpr(e Expr) {
	if e == nil {
		return
	}
	switch x := e.(type) {
	case *VarExpr:
		ix.nextID(x)
	case *NewExpr:
		ix.nextID(x)
		if _, ok := ix.objects[x]; !ok {
			ix.objects[x] = firstAllocationID + ObjectID(len(ix.sites))
			ix.sites = append(ix.sites, x)
		}
	case *StringExpr:
		ix.nextID(x)
	case *ExternalStringExpr:
		ix.nextID(x)
	case *FieldExpr:
		ix.nextID(x)
		ix.indexExpr(x.Base)
		ix.indexExpr(x.Key)
	case *EqExpr:
		ix.nextID(x)
		ix.indexExpr(x.Left)
		ix.indexExpr(x.Right)
	case *NotExpr:
		ix.nextID(x)
		ix.indexExpr(x.X)
	case *AndExpr:
		ix.nextID(x)
		ix.indexExpr(x.Left)
		ix.indexExpr(x.Right)
	case *OrExpr:
		ix.nextID(x)
		ix.indexExpr(x.Left)
		ix.indexExpr(x.Right)
	}
}

func (ix *programIndex) objectRef(id ObjectID) ObjectRef {
	switch id {
	case SharedPrototypeID:
		return ObjectRef{ID: id, Kind: KindSharedPrototype, Label: "shared prototype"}
	case ConstructorID:
		return ObjectRef{ID: id, Kind: KindConstructor, Label: "constructor object"}
	}
	idx := int(id - firstAllocationID)
	if idx >= 0 && idx < len(ix.sites) {
		n := ix.sites[idx]
		label := n.Label
		if label == "" {
			label = fmt.Sprintf("object@%d", ix.nodeIDs[n])
		}
		return ObjectRef{
			ID:     id,
			Kind:   KindAllocated,
			Label:  label,
			NodeID: ix.nodeIDs[n],
		}
	}
	return ObjectRef{ID: id, Kind: KindAllocated, Label: fmt.Sprintf("object#%d", id)}
}

// ErrInvalidProgram indicates that the script AST is outside the supported
// restricted language.
var ErrInvalidProgram = errors.New("invalid restricted program")

// Validate checks the syntactic restrictions that make the analysis sound:
// the finite string alphabet, at most 32 object allocations, and loop bounds
// of at most 16.
func Validate(p *Program) error {
	if p == nil {
		return fmt.Errorf("%w: nil program", ErrInvalidProgram)
	}
	v := &validator{}
	for _, s := range p.Stmts {
		v.stmt(s)
	}
	if len(v.objects) > MaxObjects {
		return fmt.Errorf("%w: %d object allocations, maximum is %d",
			ErrInvalidProgram, len(v.objects), MaxObjects)
	}
	if v.err != nil {
		return v.err
	}
	return nil
}

type validator struct {
	objects map[*NewExpr]struct{}
	err     error
}

func (v *validator) failf(format string, args ...any) {
	if v.err == nil {
		v.err = fmt.Errorf("%w: %s", ErrInvalidProgram, fmt.Sprintf(format, args...))
	}
}

func (v *validator) stmt(s Stmt) {
	if v.err != nil {
		return
	}
	if s == nil {
		v.failf("nil statement")
		return
	}
	switch x := s.(type) {
	case *AssignStmt:
		if x.Value == nil {
			v.failf("assignment without value")
			return
		}
		switch target := x.Target.(type) {
		case *VarExpr:
			if target.Name == "" {
				v.failf("empty variable name")
			}
		case *FieldExpr:
			v.expr(target)
		default:
			if x.Target == nil {
				v.failf("assignment without target")
			} else {
				v.failf("assignment target must be a variable or field, got %T", x.Target)
			}
		}
		v.expr(x.Value)
	case *ExprStmt:
		if x.X == nil {
			v.failf("empty expression statement")
		} else {
			v.expr(x.X)
		}
	case *IfStmt:
		if x.Cond == nil {
			v.failf("if without condition")
		} else {
			v.expr(x.Cond)
		}
		for _, inner := range x.Then {
			v.stmt(inner)
		}
		for _, inner := range x.Else {
			v.stmt(inner)
		}
	case *WhileStmt:
		if x.MaxIterations < 0 || x.MaxIterations > MaxLoopIterations {
			v.failf("while bound %d outside [0,%d]", x.MaxIterations, MaxLoopIterations)
		}
		if x.Cond == nil {
			v.failf("while without condition")
		} else {
			v.expr(x.Cond)
		}
		for _, inner := range x.Body {
			v.stmt(inner)
		}
	case *RepeatStmt:
		if x.Times < 0 || x.Times > MaxLoopIterations {
			v.failf("repeat count %d outside [0,%d]", x.Times, MaxLoopIterations)
		}
		for _, inner := range x.Body {
			v.stmt(inner)
		}
	default:
		v.failf("unsupported statement %T", s)
	}
}

func (v *validator) expr(e Expr) {
	if v.err != nil {
		return
	}
	if e == nil {
		v.failf("nil expression")
		return
	}
	switch x := e.(type) {
	case *VarExpr:
		if x.Name == "" {
			v.failf("empty variable name")
		}
	case *NewExpr:
		if v.objects == nil {
			v.objects = make(map[*NewExpr]struct{})
		}
		v.objects[x] = struct{}{}
	case *StringExpr:
		if !InStringDomain(x.Value) {
			v.failf("string constant %q is outside the allowed alphabet", x.Value)
		}
	case *ExternalStringExpr:
		// Any value in the finite alphabet is allowed at runtime.
	case *FieldExpr:
		v.expr(x.Base)
		v.expr(x.Key)
	case *EqExpr:
		v.expr(x.Left)
		v.expr(x.Right)
	case *NotExpr:
		v.expr(x.X)
	case *AndExpr:
		v.expr(x.Left)
		v.expr(x.Right)
	case *OrExpr:
		v.expr(x.Left)
		v.expr(x.Right)
	default:
		v.failf("unsupported expression %T", e)
	}
}
