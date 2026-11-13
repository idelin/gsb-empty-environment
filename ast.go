package netsec

import "strings"

// Node is one node in a function reference AST. Only nodes defined by this
// package can be used, which keeps the resolver's security-critical traversal
// closed over the supported object-reference forms.
type Node interface {
	node()
}

// AST is an ordered sequence of nodes. A Ref node is security critical by
// definition: table, function, and operator refs are all frozen at publish
// time.
type AST []Node

// Ref is an object reference. SchemaID is optional; when empty the name is
// resolved through the frozen search path. Signature selects a function or
// operator overload. Args are evaluated as part of the body and are therefore
// also resolved in the definer's frozen context.
type Ref struct {
	Kind      ObjectKind
	SchemaID  SchemaID
	Name      string
	Signature string
	ArgTypes  []string
	Args      AST
}

func (Ref) node() {}

// Seq groups nodes for readability.
type Seq struct {
	Nodes AST
}

func (Seq) node() {}

// Const is a literal value.
type Const struct {
	Value any
}

func (Const) node() {}

// Param reads a pre-evaluated caller argument. Parameter expressions are
// evaluated by the host before any frozen object is executed with definer
// privileges.
type Param struct {
	Index int
}

func (Param) node() {}

// DynamicSQL models dynamic SQL. At execution it is denied unless the caller
// supplies a trusted AST under Key (or SQL when Key is empty). That AST is
// resolved again against the function's frozen search path.
type DynamicSQL struct {
	SQL string
	Key string
}

func (DynamicSQL) node() {}

// TableRef constructs an unqualified table reference.
func TableRef(name string) Ref {
	return Ref{Kind: KindTable, Name: name}
}

// QualifiedTableRef constructs an explicitly qualified table reference.
func QualifiedTableRef(schema SchemaID, name string) Ref {
	return Ref{Kind: KindTable, SchemaID: schema, Name: name}
}

// FunctionRef constructs an unqualified overloaded function reference.
func FunctionRef(name, signature string, args ...Node) Ref {
	return Ref{Kind: KindFunction, Name: name, Signature: signature, Args: AST(args)}
}

// QualifiedFunctionRef constructs an explicitly qualified function reference.
func QualifiedFunctionRef(schema SchemaID, name, signature string, args ...Node) Ref {
	return Ref{Kind: KindFunction, SchemaID: schema, Name: name, Signature: signature, Args: AST(args)}
}

// OperatorRef constructs an unqualified operator reference.
func OperatorRef(signature string, args ...Node) Ref {
	name, sig := splitOperatorSignature(signature)
	return Ref{Kind: KindOperator, Name: name, Signature: sig, Args: AST(args)}
}

// QualifiedOperatorRef constructs an explicitly qualified operator reference.
func QualifiedOperatorRef(schema SchemaID, signature string, args ...Node) Ref {
	name, sig := splitOperatorSignature(signature)
	return Ref{Kind: KindOperator, SchemaID: schema, Name: name, Signature: sig, Args: AST(args)}
}

// Params returns parameter placeholder nodes for the given indexes.
func Params(indexes ...int) AST {
	out := make(AST, 0, len(indexes))
	for _, index := range indexes {
		out = append(out, Param{Index: index})
	}
	return out
}

// Sequence combines nodes.
func Sequence(nodes ...Node) Seq {
	return Seq{Nodes: AST(nodes)}
}

func splitOperatorSignature(signature string) (name, sig string) {
	if i := strings.Index(signature, "("); i > 0 {
		return signature[:i], signature[i:]
	}
	return signature, signature
}
