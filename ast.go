package netsec

import "sync/atomic"

// Expr is an immutable expression node. The unexported marker means that
// callers can compose the provided nodes but cannot introduce a new node type
// whose evaluation bypasses the executor's label rules.
type Expr interface {
	isExpr()
}

type constExpr struct{ value int }
type sourceExpr struct{ source Source }
type addExpr struct{ left, right Expr }
type conditionalExpr struct{ condition, then, otherwise Expr }
type letExpr struct {
	name  string
	value Expr
	body  Expr
}
type varExpr struct{ name string }
type outputExpr struct{ value Expr }

func (constExpr) isExpr()       {}
func (sourceExpr) isExpr()      {}
func (addExpr) isExpr()         {}
func (conditionalExpr) isExpr() {}
func (letExpr) isExpr()         {}
func (varExpr) isExpr()         {}
func (outputExpr) isExpr()      {}

// Const constructs an integer constant.
func Const(value int) Expr { return constExpr{value: value} }

// Secret constructs a read of a secret source. An invalid or missing source
// makes execution fail rather than silently producing an unlabelled value.
func Secret(source Source) Expr { return sourceExpr{source: source} }

// Add constructs integer addition. The result depends on both operands.
func Add(left, right Expr) Expr { return addExpr{left: left, right: right} }

// If constructs a conditional. The condition label becomes part of the
// program-counter label of the selected branch, so it taints branch results
// even when the branch itself contains only constants or a declassification.
func If(condition, then, otherwise Expr) Expr {
	return conditionalExpr{condition: condition, then: then, otherwise: otherwise}
}

// Let binds a local variable for the duration of body. The binding is lexical
// and immutable inside body.
func Let(name string, value, body Expr) Expr {
	return letExpr{name: name, value: value, body: body}
}

// Var reads a local variable bound by an enclosing Let.
func Var(name string) Expr { return varExpr{name: name} }

// Output publishes one integer. Execution succeeds only if its label is empty.
func Output(value Expr) Expr { return outputExpr{value: value} }

var declassifyIDs atomic.Uint64

// declassifyCore is the immutable identity and expression of a declassify
// site. Authorizations are bound to this pointer, not to a caller-supplied ID.
type declassifyCore struct {
	id   uint64
	expr Expr
}

// DeclassifyNode is the sole declassification operation. The node's
// expression is immutable. An authorization is attached with
// WithAuthorization after the trusted registrar issues it for this node.
type DeclassifyNode struct {
	core *declassifyCore
	auth Authorization
}

func (*DeclassifyNode) isExpr() {}

// Declassify constructs a declassification site around expr. The returned
// node has no authorization and cannot execute until one is attached.
func Declassify(expr Expr) *DeclassifyNode {
	return &DeclassifyNode{
		core: &declassifyCore{
			id:   declassifyIDs.Add(1),
			expr: expr,
		},
	}
}

// WithAuthorization returns a copy of the site carrying auth. The copy shares
// the same immutable node identity and expression, so moving or copying the
// node does not change what the authorization is bound to. The registrar still
// checks the request, node identity, allowed sources, and one-time use.
func (d *DeclassifyNode) WithAuthorization(auth Authorization) *DeclassifyNode {
	if d == nil {
		return nil
	}
	return &DeclassifyNode{core: d.core, auth: auth}
}

// Authorization returns the attached authorization, if any.
func (d *DeclassifyNode) Authorization() Authorization {
	if d == nil {
		return nil
	}
	return d.auth
}

// NodeID returns the stable identity used in audit events.
func (d *DeclassifyNode) NodeID() uint64 {
	if d == nil || d.core == nil {
		return 0
	}
	return d.core.id
}
