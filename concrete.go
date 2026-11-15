package netsec

import (
	"errors"
	"fmt"
	"sort"
)

// ErrTooManyObjects is returned by the concrete executor when a run creates
// more than MaxObjects objects.
var ErrTooManyObjects = errors.New("too many runtime objects")

// ErrMissingExternal is returned when an external string has no chosen value.
var ErrMissingExternal = errors.New("missing external string choice")

// ErrExecutionLimit is returned when an exhaustive check would exceed its
// configured number of concrete executions.
var ErrExecutionLimit = errors.New("concrete execution limit exceeded")

type concreteKind int

const (
	concreteUndefined concreteKind = iota
	concreteString
	concreteObject
	concreteBool
)

type concreteValue struct {
	kind concreteKind
	str  string
	obj  *runtimeObject
	b    bool
}

func undefinedValue() concreteValue { return concreteValue{kind: concreteUndefined} }
func stringValue(s string) concreteValue {
	return concreteValue{kind: concreteString, str: s}
}
func objectValue(o *runtimeObject) concreteValue {
	return concreteValue{kind: concreteObject, obj: o}
}
func boolValue(b bool) concreteValue {
	return concreteValue{kind: concreteBool, b: b}
}

type runtimeObject struct {
	fields map[string]concreteValue
	proto  *runtimeObject
}

// ConcreteWrite is one concrete execution's write to the shared prototype.
type ConcreteWrite struct {
	Node   *AssignStmt
	NodeID int
	Key    string
}

// Execution is the result of one concrete run. Choices records the external
// strings used by that run.
type Execution struct {
	Dangerous      []ConcreteWrite
	ObjectsCreated int
	Choices        map[*ExternalStringExpr]string
}

// DangerousNodes returns the unique dangerous write node IDs.
func (e *Execution) DangerousNodes() []int {
	if e == nil {
		return nil
	}
	set := make(map[int]struct{}, len(e.Dangerous))
	for _, w := range e.Dangerous {
		set[w.NodeID] = struct{}{}
	}
	out := make([]int, 0, len(set))
	for id := range set {
		out = append(out, id)
	}
	sort.Ints(out)
	return out
}

// Execute runs a program concretely with one fixed value for every external
// string occurrence. The executor uses the same prototype model as the
// analyzer:
//
//   - every allocated object starts with the shared prototype;
//   - reading constructor reaches a fixed constructor object whose prototype
//     is the shared prototype;
//   - writing __proto__ replaces the receiver's prototype for object values;
//   - writing any property of the shared prototype is dangerous.
func Execute(p *Program, choices map[*ExternalStringExpr]string) (*Execution, error) {
	if err := Validate(p); err != nil {
		return nil, err
	}
	ix := indexProgram(p)
	exec := &Execution{Choices: make(map[*ExternalStringExpr]string)}
	r := &concreteRunner{
		ix:      ix,
		exec:    exec,
		env:     make(map[string]concreteValue),
		choices: make(map[*ExternalStringExpr]string, len(choices)),
	}
	for site, value := range choices {
		if !InStringDomain(value) {
			return nil, fmt.Errorf("%w: external %s has value %q",
				ErrInvalidProgram, site.Label, value)
		}
		r.choices[site] = value
	}

	shared := &runtimeObject{fields: make(map[string]concreteValue)}
	ctor := &runtimeObject{fields: make(map[string]concreteValue), proto: shared}
	shared.fields["constructor"] = objectValue(ctor)
	ctor.fields["prototype"] = objectValue(shared)
	r.shared = shared
	r.ctor = ctor

	for _, s := range p.Stmts {
		if err := r.stmt(s); err != nil {
			return nil, err
		}
	}
	exec.Choices = r.choices
	exec.ObjectsCreated = r.created
	return exec, nil
}

type concreteRunner struct {
	ix      *programIndex
	exec    *Execution
	env     map[string]concreteValue
	choices map[*ExternalStringExpr]string
	shared  *runtimeObject
	ctor    *runtimeObject
	created int
}

func (r *concreteRunner) stmt(s Stmt) error {
	switch x := s.(type) {
	case *AssignStmt:
		return r.assign(x)
	case *ExprStmt:
		_, err := r.expr(x.X)
		return err
	case *IfStmt:
		cond, err := r.expr(x.Cond)
		if err != nil {
			return err
		}
		if truthy(cond) {
			return r.block(x.Then)
		}
		return r.block(x.Else)
	case *WhileStmt:
		for i := 0; i < x.MaxIterations; i++ {
			cond, err := r.expr(x.Cond)
			if err != nil {
				return err
			}
			if !truthy(cond) {
				break
			}
			if err := r.block(x.Body); err != nil {
				return err
			}
		}
		return nil
	case *RepeatStmt:
		for i := 0; i < x.Times; i++ {
			if err := r.block(x.Body); err != nil {
				return err
			}
		}
		return nil
	default:
		return fmt.Errorf("%w: unsupported statement %T", ErrInvalidProgram, s)
	}
}

func (r *concreteRunner) block(stmts []Stmt) error {
	for _, s := range stmts {
		if err := r.stmt(s); err != nil {
			return err
		}
	}
	return nil
}

func (r *concreteRunner) assign(x *AssignStmt) error {
	switch target := x.Target.(type) {
	case *VarExpr:
		v, err := r.expr(x.Value)
		if err != nil {
			return err
		}
		r.env[target.Name] = v
		return nil
	case *FieldExpr:
		base, err := r.expr(target.Base)
		if err != nil {
			return err
		}
		keyValue, err := r.expr(target.Key)
		if err != nil {
			return err
		}
		value, err := r.expr(x.Value)
		if err != nil {
			return err
		}
		if base.kind != concreteObject || base.obj == nil {
			return nil
		}
		key := propertyKey(keyValue)
		if key == "__proto__" {
			if value.kind == concreteObject && value.obj != nil {
				base.obj.proto = value.obj
			}
		} else {
			base.obj.fields[key] = value
		}
		if base.obj == r.shared {
			r.exec.Dangerous = append(r.exec.Dangerous, ConcreteWrite{
				Node:   x,
				NodeID: r.ix.nodeIDs[x],
				Key:    key,
			})
		}
		return nil
	default:
		return fmt.Errorf("%w: invalid assignment target %T", ErrInvalidProgram, x.Target)
	}
}

func (r *concreteRunner) expr(e Expr) (concreteValue, error) {
	switch x := e.(type) {
	case *VarExpr:
		if v, ok := r.env[x.Name]; ok {
			return v, nil
		}
		return undefinedValue(), nil
	case *NewExpr:
		if r.created >= MaxObjects {
			return undefinedValue(), fmt.Errorf("%w: limit is %d", ErrTooManyObjects, MaxObjects)
		}
		r.created++
		return objectValue(&runtimeObject{
			fields: make(map[string]concreteValue),
			proto:  r.shared,
		}), nil
	case *StringExpr:
		return stringValue(x.Value), nil
	case *ExternalStringExpr:
		v, ok := r.choices[x]
		if !ok {
			return undefinedValue(), fmt.Errorf("%w: %s", ErrMissingExternal, x.Label)
		}
		return stringValue(v), nil
	case *FieldExpr:
		base, err := r.expr(x.Base)
		if err != nil {
			return undefinedValue(), err
		}
		keyValue, err := r.expr(x.Key)
		if err != nil {
			return undefinedValue(), err
		}
		if base.kind != concreteObject || base.obj == nil {
			return undefinedValue(), nil
		}
		return readConcreteField(base.obj, propertyKey(keyValue)), nil
	case *EqExpr:
		left, err := r.expr(x.Left)
		if err != nil {
			return undefinedValue(), err
		}
		right, err := r.expr(x.Right)
		if err != nil {
			return undefinedValue(), err
		}
		return boolValue(concreteEqual(left, right)), nil
	case *NotExpr:
		v, err := r.expr(x.X)
		if err != nil {
			return undefinedValue(), err
		}
		return boolValue(!truthy(v)), nil
	case *AndExpr:
		left, err := r.expr(x.Left)
		if err != nil {
			return undefinedValue(), err
		}
		if !truthy(left) {
			return boolValue(false), nil
		}
		right, err := r.expr(x.Right)
		if err != nil {
			return undefinedValue(), err
		}
		return boolValue(truthy(right)), nil
	case *OrExpr:
		left, err := r.expr(x.Left)
		if err != nil {
			return undefinedValue(), err
		}
		if truthy(left) {
			return boolValue(true), nil
		}
		right, err := r.expr(x.Right)
		if err != nil {
			return undefinedValue(), err
		}
		return boolValue(truthy(right)), nil
	default:
		return undefinedValue(), fmt.Errorf("%w: unsupported expression %T", ErrInvalidProgram, e)
	}
}

func readConcreteField(o *runtimeObject, key string) concreteValue {
	if key == "__proto__" {
		if o.proto == nil {
			return undefinedValue()
		}
		return objectValue(o.proto)
	}
	visited := make(map[*runtimeObject]bool)
	for cur := o; cur != nil; cur = cur.proto {
		if visited[cur] {
			break
		}
		visited[cur] = true
		if v, ok := cur.fields[key]; ok {
			return v
		}
	}
	return undefinedValue()
}

func propertyKey(v concreteValue) string {
	switch v.kind {
	case concreteString:
		return v.str
	case concreteBool:
		if v.b {
			return "true"
		}
		return "false"
	case concreteObject:
		return "[object Object]"
	default:
		return "undefined"
	}
}

func truthy(v concreteValue) bool {
	switch v.kind {
	case concreteUndefined:
		return false
	case concreteBool:
		return v.b
	case concreteString:
		return v.str != ""
	case concreteObject:
		return v.obj != nil
	default:
		return false
	}
}

func concreteEqual(a, b concreteValue) bool {
	if a.kind != b.kind {
		return false
	}
	switch a.kind {
	case concreteUndefined:
		return true
	case concreteString:
		return a.str == b.str
	case concreteObject:
		return a.obj == b.obj
	case concreteBool:
		return a.b == b.b
	default:
		return false
	}
}

// ExternalSites returns all external-string occurrences in evaluation order.
func ExternalSites(p *Program) []*ExternalStringExpr {
	var out []*ExternalStringExpr
	seen := make(map[*ExternalStringExpr]bool)
	var walkStmt func(Stmt)
	var walkExpr func(Expr)
	walkExpr = func(e Expr) {
		switch x := e.(type) {
		case *ExternalStringExpr:
			if !seen[x] {
				seen[x] = true
				out = append(out, x)
			}
		case *FieldExpr:
			walkExpr(x.Base)
			walkExpr(x.Key)
		case *EqExpr:
			walkExpr(x.Left)
			walkExpr(x.Right)
		case *NotExpr:
			walkExpr(x.X)
		case *AndExpr:
			walkExpr(x.Left)
			walkExpr(x.Right)
		case *OrExpr:
			walkExpr(x.Left)
			walkExpr(x.Right)
		}
	}
	walkStmt = func(s Stmt) {
		switch x := s.(type) {
		case *AssignStmt:
			walkExpr(x.Target)
			walkExpr(x.Value)
		case *ExprStmt:
			walkExpr(x.X)
		case *IfStmt:
			walkExpr(x.Cond)
			for _, inner := range x.Then {
				walkStmt(inner)
			}
			for _, inner := range x.Else {
				walkStmt(inner)
			}
		case *WhileStmt:
			walkExpr(x.Cond)
			for _, inner := range x.Body {
				walkStmt(inner)
			}
		case *RepeatStmt:
			for _, inner := range x.Body {
				walkStmt(inner)
			}
		}
	}
	if p != nil {
		for _, s := range p.Stmts {
			walkStmt(s)
		}
	}
	return out
}
