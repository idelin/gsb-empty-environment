// Package joinselim is the importable root facade for the join-elimination
// toolkit. The implementation and exhaustive tests live in the joinselim
// subpackage; aliases here allow importing either the module root or the
// versioned subpackage path.
package joinselim

import js "prescreen.local/ps-0f39c054e3/joinselim"

type (
	Ref                  = js.Ref
	TableRef             = js.TableRef
	Equality             = js.Equality
	JoinType             = js.JoinType
	PredicateOp          = js.PredicateOp
	Predicate            = js.Predicate
	Join                 = js.Join
	Query                = js.Query
	Column               = js.Column
	Table                = js.Table
	ForeignKey           = js.ForeignKey
	FunctionalDependency = js.FunctionalDependency
	Schema               = js.Schema
	Elimination          = js.Elimination
	Result               = js.Result
	Row                  = js.Row
	DataSet              = js.DataSet
	Tuple                = js.Tuple
)

const (
	InnerJoin = js.InnerJoin
	LeftJoin  = js.LeftJoin

	OpNotNull  = js.OpNotNull
	OpIsNull   = js.OpIsNull
	OpEqual    = js.OpEqual
	OpNotEqual = js.OpNotEqual
	OpLT       = js.OpLT
	OpLE       = js.OpLE
	OpGT       = js.OpGT
	OpGE       = js.OpGE
)

var (
	Col          = js.Col
	NotNull      = js.NotNull
	IsNull       = js.IsNull
	EqualValue   = js.EqualValue
	CompareValue = js.CompareValue
	EqualColumns = js.EqualColumns

	NewSchema = js.NewSchema

	Rewrite        = js.Rewrite
	EliminateJoins = js.EliminateJoins

	Evaluate       = js.Evaluate
	DistinctTuples = js.DistinctTuples
	TupleKey       = js.TupleKey
	Multiset       = js.Multiset
	EqualMultiset  = js.EqualMultiset
	MultisetDiff   = js.MultisetDiff
)
