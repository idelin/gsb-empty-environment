package plancache

import (
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"strconv"
	"strings"
)

// Digest is a content-derived fingerprint (for example a canonical AST
// digest, a role-closure digest or an RLS policy digest).
type Digest string

// PrincipalID identifies the calling security principal.
type PrincipalID string

// ObjectID is the stable, unique identity of a base table. A DROP followed
// by a same-name CREATE always yields a new ObjectID, so name-based plan
// reuse across recreation is impossible.
type ObjectID string

// ViewID is the stable, unique identity of a view definition.
type ViewID string

// Operation is a privilege/action required on a referenced object, such as
// "SELECT", "INSERT", "UPDATE" or "DELETE".
type Operation string

// DependencyNode is one node in the fully expanded dependency graph.
// Exactly one of View or Table is populated.
type DependencyNode struct {
	RefType string // "view" or "table"

	// View fields.
	View ViewRef

	// Table fields.
	Table TableRef
}

// ViewRef captures the immutable identity of a nested view as expanded at
// compile time.
type ViewRef struct {
	ViewID          ViewID
	ViewDefVersion  int64
	ParentViewID    ViewID // empty for the top-level view
	UnderlyingTable ObjectID
}

// TableRef captures the immutable identity and current policy state of a
// base table as expanded at compile time.
type TableRef struct {
	ObjectID      ObjectID
	SchemaVersion int64
	RLSPolicy     Digest // digest of the table's effective RLS policy; empty means no RLS
}

// CompileInput is everything required to compile and authorize a plan.
//
// It intentionally contains no SQL text and no relation/view names. Plans
// are therefore structurally incapable of being reused by SQL text or view
// name alone; reuse requires identity- and version-level agreement across
// the whole expanded dependency graph.
type CompileInput struct {
	// ASTDigest is the canonicalized query AST summary (canonical, so
	// semantically identical queries share the value).
	ASTDigest Digest

	// ParamTypes is the ordered list of parameter types. Parameter values
	// never appear here and never enter plan identity.
	ParamTypes []string

	// Caller is the principal the plan is compiled for.
	Caller PrincipalID

	// RoleClosure is the digest of the caller's transitive role set.
	RoleClosure Digest

	// DepGraph is the fully expanded dependency graph in deterministic
	// expansion order (outermost view first, base tables last).
	DepGraph []DependencyNode

	// RequiredOps are the operations needed, in deterministic order.
	RequiredOps []Operation

	// RLSPolicies maps every referenced base table ObjectID to its RLS
	// policy digest at compile time.
	RLSPolicies map[ObjectID]Digest

	// PolicyEpoch is the host-global RLS policy generation. Any RLS
	// change advances it.
	PolicyEpoch int64
}

// CacheKey is the full, security-sensitive identity of a compiled plan.
// All authorization-relevant dimensions are first-class key fields; there
// is no SQL text and no relation name field.
type CacheKey struct {
	ASTDigest   Digest
	ParamTypes  []string
	Caller      PrincipalID
	RoleClosure Digest
	DepGraph    []DependencyNode
	RequiredOps []Operation
	RLSPolicies map[ObjectID]Digest
	PolicyEpoch int64
}

// KeyOf derives the full CacheKey for a compile input.
func KeyOf(in CompileInput) CacheKey {
	graph := make([]DependencyNode, len(in.DepGraph))
	copy(graph, in.DepGraph)
	ops := make([]Operation, len(in.RequiredOps))
	copy(ops, in.RequiredOps)
	params := make([]string, len(in.ParamTypes))
	copy(params, in.ParamTypes)
	rls := make(map[ObjectID]Digest, len(in.RLSPolicies))
	for k, v := range in.RLSPolicies {
		rls[k] = v
	}
	return CacheKey{
		ASTDigest:   in.ASTDigest,
		ParamTypes:  params,
		Caller:      in.Caller,
		RoleClosure: in.RoleClosure,
		DepGraph:    graph,
		RequiredOps: ops,
		RLSPolicies: rls,
		PolicyEpoch: in.PolicyEpoch,
	}
}

func writeStr(b *strings.Builder, s string) {
	b.WriteString(strconv.Itoa(len(s)))
	b.WriteByte(':')
	b.WriteString(s)
	b.WriteByte(';')
}

func writeStrings(b *strings.Builder, label string, ss []string) {
	writeStr(b, label)
	b.WriteByte('[')
	for _, s := range ss {
		writeStr(b, s)
	}
	b.WriteByte(']')
}

// canonical renders the key into a deterministic, length-prefixed encoding.
// Maps are emitted in sorted key order, so two structurally equal keys share
// the encoding regardless of Go map iteration order.
func (k CacheKey) canonical() string {
	var b strings.Builder
	writeStr(&b, "ast")
	writeStr(&b, string(k.ASTDigest))
	writeStrings(&b, "params", k.ParamTypes)
	writeStr(&b, "caller")
	writeStr(&b, string(k.Caller))
	writeStr(&b, "roles")
	writeStr(&b, string(k.RoleClosure))

	writeStr(&b, "graph")
	b.WriteByte('[')
	for _, n := range k.DepGraph {
		writeStr(&b, n.RefType)
		if n.RefType == "view" {
			writeStr(&b, "v")
			writeStr(&b, string(n.View.ViewID))
			writeStr(&b, strconv.FormatInt(n.View.ViewDefVersion, 10))
			writeStr(&b, string(n.View.ParentViewID))
			writeStr(&b, string(n.View.UnderlyingTable))
		} else {
			writeStr(&b, "t")
			writeStr(&b, string(n.Table.ObjectID))
			writeStr(&b, strconv.FormatInt(n.Table.SchemaVersion, 10))
			writeStr(&b, string(n.Table.RLSPolicy))
		}
	}
	b.WriteByte(']')

	writeStr(&b, "ops")
	b.WriteByte('[')
	for _, op := range k.RequiredOps {
		writeStr(&b, string(op))
	}
	b.WriteByte(']')

	ids := make([]string, 0, len(k.RLSPolicies))
	byID := make(map[string]Digest, len(k.RLSPolicies))
	for id, d := range k.RLSPolicies {
		s := string(id)
		ids = append(ids, s)
		byID[s] = d
	}
	sort.Strings(ids)
	writeStr(&b, "rls")
	b.WriteByte('[')
	for _, id := range ids {
		writeStr(&b, id)
		writeStr(&b, string(byID[id]))
	}
	b.WriteByte(']')

	writeStr(&b, "epoch")
	writeStr(&b, strconv.FormatInt(k.PolicyEpoch, 10))
	return b.String()
}

// Hash returns the content hash of the full key. Two plans may share a key
// only when every authorization-relevant dimension is identical.
func (k CacheKey) Hash() string {
	sum := sha256.Sum256([]byte(k.canonical()))
	return hex.EncodeToString(sum[:])
}

// Operator is an executable physical-plan step. Func is invoked only after
// the plan has passed full pre-execution revalidation.
type Operator struct {
	Name string
	// Func receives the structured, type-checked parameter bindings.
	Func func(bindings []any) error
}

// Manifest is the complete immutable dependency list captured at compile
// time. It drives every pre-execution revalidation check.
type Manifest struct {
	Key         CacheKey
	Views       []ViewRef
	Tables      []TableRef
	RequiredOps []Operation
	PolicyEpoch int64
	Caller      PrincipalID
	RoleClosure Digest
}

// Plan is a compiled, immutable execution plan.
type Plan struct {
	ID        string
	Key       CacheKey
	Manifest  Manifest
	Operators []Operator
}
