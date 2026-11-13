package netsec

import (
	"fmt"
	"sort"
	"strings"
)

// PrincipalID is the stable identity of a database principal. Names may be
// reused by a database, so callers should use the catalog's stable identifier.
type PrincipalID string

// ObjectID is the stable identity of a database object. It is deliberately
// separate from the object's name: dropping and recreating a table with the
// same name must be observable as a different object generation.
type ObjectID string

// Operation is one database privilege bit.
type Operation uint16

const (
	OpSelect Operation = 1 << iota
	OpInsert
	OpUpdate
	OpDelete
	OpTruncate
)

// OperationSet is a compact set of Operation values.
type OperationSet uint16

const (
	OpsSelect   = OperationSet(OpSelect)
	OpsInsert   = OperationSet(OpInsert)
	OpsUpdate   = OperationSet(OpUpdate)
	OpsDelete   = OperationSet(OpDelete)
	OpsTruncate = OperationSet(OpTruncate)

	OpsRead  = OpsSelect
	OpsWrite = OpsInsert | OpsUpdate | OpsDelete | OpsTruncate
	OpsAll   = OpsRead | OpsWrite
)

// NewOperationSet combines operations into a set.
func NewOperationSet(ops ...Operation) OperationSet {
	var set OperationSet
	for _, op := range ops {
		set |= OperationSet(op)
	}
	return set
}

// Has reports whether all bits in op are present.
func (s OperationSet) Has(op Operation) bool {
	return s&OperationSet(op) == OperationSet(op)
}

// Contains reports whether all requested operations are present.
func (s OperationSet) Contains(other OperationSet) bool {
	return s&other == other
}

// IsZero reports whether no operation is requested.
func (s OperationSet) IsZero() bool { return s == 0 }

// String returns a stable, sorted representation used by cache keys.
func (s OperationSet) String() string {
	if s == 0 {
		return "none"
	}
	var names []string
	for _, item := range []struct {
		op   Operation
		name string
	}{
		{OpSelect, "select"},
		{OpInsert, "insert"},
		{OpUpdate, "update"},
		{OpDelete, "delete"},
		{OpTruncate, "truncate"},
	} {
		if s.Has(item.op) {
			names = append(names, item.name)
		}
	}
	remaining := uint16(s)
	for _, item := range []Operation{OpSelect, OpInsert, OpUpdate, OpDelete, OpTruncate} {
		remaining &^= uint16(item)
	}
	if remaining != 0 {
		names = append(names, fmt.Sprintf("0x%x", remaining))
	}
	return strings.Join(names, ",")
}

// ParamType is the wire type of one bind parameter. Parameter values are
// intentionally absent from plan identity.
type ParamType string

const (
	ParamInt     ParamType = "int"
	ParamInt32   ParamType = "int32"
	ParamInt64   ParamType = "int64"
	ParamUint    ParamType = "uint"
	ParamFloat64 ParamType = "float64"
	ParamText    ParamType = "text"
	ParamString  ParamType = "string"
	ParamBool    ParamType = "bool"
	ParamBytes   ParamType = "bytes"
	ParamAny     ParamType = "any"
)

// ObjectDependency is one object generation and policy digest referenced by a
// compiled plan. Operations is the complete set of operations the plan needs
// on the object.
type ObjectDependency struct {
	ObjectID      ObjectID
	SchemaVersion uint64
	Operations    OperationSet
	// RequiredOps is an alias for Operations.
	RequiredOps OperationSet
	RLSDigest   string
}

// NewObjectDependency constructs a normalized dependency.
func NewObjectDependency(id ObjectID, schemaVersion uint64, ops OperationSet, rlsDigest string) ObjectDependency {
	return ObjectDependency{
		ObjectID:      id,
		SchemaVersion: schemaVersion,
		Operations:    ops,
		RLSDigest:     rlsDigest,
	}
}

// Version is a convenience accessor for consumers that call the schema
// generation "version".
func (d ObjectDependency) Version() uint64 { return d.SchemaVersion }

// Op is a convenience accessor for the object's required operations.
func (d ObjectDependency) Op() OperationSet { return dependencyOps(d) }

// CompileInput is every input that can influence authorization or execution.
// It deliberately contains no parameter values.
//
// QueryASTDigest and ASTDigest are aliases; ParamTypeDigest and
// ParamTypeListDigest are aliases; Objects and ObjectDependencies are aliases.
// Supplying the same value through both aliases is allowed. Supplying
// conflicting values is rejected by Validate/Key.
type CompileInput struct {
	QueryASTDigest          string
	ASTDigest               string
	CanonicalASTDigest      string
	CanonicalQueryASTDigest string

	ParamTypes          []ParamType
	ParamTypeDigest     string
	ParamTypeListDigest string

	PrincipalID       PrincipalID
	Roles             []string
	RoleClosureDigest string

	Objects            []ObjectDependency
	ObjectDependencies []ObjectDependency

	PolicyEpoch uint64
}

// ObjectRef identifies an object and the operations a query needs. The host
// resolves the version, RLS digest and principal authorization from its
// pinned snapshot before compiling.
type ObjectRef struct {
	ObjectID   ObjectID
	Operations OperationSet
}

// Query is the authorization-relevant description of a statement. It contains
// no parameter values.
type Query struct {
	ASTDigest       string
	ParamTypes      []ParamType
	ParamTypeDigest string
	Objects         []ObjectRef
}

// PlanKey is the complete identity of a compiled plan.
type PlanKey string

func (k PlanKey) String() string { return string(k) }

// OperatorKind identifies an immutable execution primitive.
type OperatorKind uint8

const (
	OperatorQuery OperatorKind = iota + 1
	OperatorScan
	OperatorIndexScan
	OperatorFilter
	OperatorRowSecurity
	OperatorWrite
	OperatorProject
	OperatorAggregate
)

func (k OperatorKind) String() string {
	switch k {
	case OperatorQuery:
		return "query"
	case OperatorScan:
		return "scan"
	case OperatorIndexScan:
		return "index_scan"
	case OperatorFilter:
		return "filter"
	case OperatorRowSecurity:
		return "row_security"
	case OperatorWrite:
		return "write"
	case OperatorProject:
		return "project"
	case OperatorAggregate:
		return "aggregate"
	default:
		return fmt.Sprintf("operator(%d)", k)
	}
}

// Operator is an immutable-by-construction execution primitive. All fields are
// unexported and every accessor returns a value.
type Operator struct {
	kind   OperatorKind
	detail string
}

// NewOperator constructs an operator. The detail is opaque to the cache and
// must not contain parameter values.
func NewOperator(kind OperatorKind, detail string) Operator {
	return Operator{kind: kind, detail: detail}
}

func (o Operator) Kind() OperatorKind { return o.kind }
func (o Operator) Detail() string     { return o.detail }

func (o Operator) String() string {
	if o.detail == "" {
		return o.kind.String()
	}
	return o.kind.String() + ":" + o.detail
}

// Plan is an immutable compiled plan. The dependency list and operator list
// are copied at construction and copied again on every accessor call.
type Plan struct {
	key   PlanKey
	input CompileInput
	deps  []ObjectDependency
	ops   []Operator
}

// CompiledPlan is an alias for Plan.
type CompiledPlan = Plan

// PlanDependency is an alias for ObjectDependency.
type PlanDependency = ObjectDependency

func newPlan(input CompileInput, key PlanKey, ops []Operator) *Plan {
	deps := cloneDependencies(input.Objects)
	return &Plan{
		key:   key,
		input: cloneCompileInput(input),
		deps:  deps,
		ops:   append([]Operator(nil), ops...),
	}
}

// Key returns the complete plan key.
func (p *Plan) Key() PlanKey { return p.key }

// CacheKey returns the complete plan key.
func (p *Plan) CacheKey() PlanKey { return p.key }

// Input returns a deep copy of the compile input.
func (p *Plan) Input() CompileInput { return cloneCompileInput(p.input) }

// Dependencies returns a copy of the object dependency manifest.
func (p *Plan) Dependencies() []ObjectDependency {
	return cloneDependencies(p.deps)
}

// Deps is an alias for Dependencies.
func (p *Plan) Deps() []ObjectDependency { return p.Dependencies() }

// ObjectDependencies is an alias for Dependencies.
func (p *Plan) ObjectDependencies() []ObjectDependency { return p.Dependencies() }

// Operators returns a copy of the immutable operator slice. Operator values
// themselves contain only unexported fields and cannot be modified.
func (p *Plan) Operators() []Operator {
	return append([]Operator(nil), p.ops...)
}

// Ops is an alias for Operators.
func (p *Plan) Ops() []Operator { return p.Operators() }

// OperatorCount returns the number of operators in the plan.
func (p *Plan) OperatorCount() int { return len(p.ops) }

// PolicyEpoch returns the policy epoch captured by the plan.
func (p *Plan) PolicyEpoch() uint64 { return p.input.PolicyEpoch }

// PrincipalID returns the principal bound into the plan identity.
func (p *Plan) PrincipalID() PrincipalID { return p.input.PrincipalID }

// RoleClosureDigest returns the role closure digest bound into the plan.
func (p *Plan) RoleClosureDigest() string { return p.input.RoleClosureDigest }

func cloneCompileInput(in CompileInput) CompileInput {
	out := in
	out.ParamTypes = append([]ParamType(nil), in.ParamTypes...)
	out.Roles = append([]string(nil), in.Roles...)
	out.Objects = cloneDependencies(in.Objects)
	out.ObjectDependencies = cloneDependencies(in.ObjectDependencies)
	return out
}

func cloneDependencies(in []ObjectDependency) []ObjectDependency {
	if len(in) == 0 {
		return nil
	}
	out := make([]ObjectDependency, len(in))
	copy(out, in)
	return out
}

func sortedStrings(in []string) []string {
	out := append([]string(nil), in...)
	sort.Strings(out)
	return out
}
