// Package configeval evaluates configuration variables that may reference
// other variables and an explicit environment snapshot.
//
// The evaluator NEVER reads the runtime process environment: all environment
// values come from the snapshot supplied via New / SetEnv.
package configeval

import "fmt"

// Kind is the type of a Value.
type Kind int

const (
	StringKind Kind = iota
	IntKind
)

// Value is the result of evaluating an expression.
type Value struct {
	Kind Kind
	Str  string
	Int  int
}

// StringValue builds a string Value.
func StringValue(s string) Value { return Value{Kind: StringKind, Str: s} }

// IntValue builds an int Value.
func IntValue(i int) Value { return Value{Kind: IntKind, Int: i} }

func (v Value) String() string {
	if v.Kind == IntKind {
		return fmt.Sprintf("%d", v.Int)
	}
	return v.Str
}

// fingerprint is the canonical string used in dependency digests.
func (v Value) fingerprint() string {
	if v.Kind == IntKind {
		return fmt.Sprintf("int:%d", v.Int)
	}
	return fmt.Sprintf("str:%q", v.Str)
}

// Expr is a configuration expression node.
type Expr interface{ exprNode() }

type litExpr struct{ v Value }
type refExpr struct{ name string }
type envExpr struct{ key string }
type concatExpr struct{ parts []Expr }
type addExpr struct{ terms []Expr }

func (litExpr) exprNode()    {}
func (refExpr) exprNode()    {}
func (envExpr) exprNode()    {}
func (concatExpr) exprNode() {}
func (addExpr) exprNode()    {}

// Str is a string literal expression.
func Str(s string) Expr { return litExpr{StringValue(s)} }

// Int is an int literal expression.
func Int(i int) Expr { return litExpr{IntValue(i)} }

// Ref references another configuration variable by name.
func Ref(name string) Expr { return refExpr{name} }

// Env references a key of the explicit environment snapshot.
// It never consults the runtime process environment.
func Env(key string) Expr { return envExpr{key} }

// Concat concatenates the string values of its parts.
// A non-string part yields a type error.
func Concat(parts ...Expr) Expr { return concatExpr{parts} }

// Add sums the int values of its terms.
// A non-int term yields a type error.
func Add(terms ...Expr) Expr { return addExpr{terms} }
