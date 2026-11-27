// Package scim 是模块根包 formal.local/w27-4e139ba19e 的薄再导出层，
// 让宿主也可以使用习惯的 “…/scim” 导入路径。全部实现与测试位于模块根包，
// 类型均为别名，因此两个导入路径共享完全相同的 API 与错误标识。
package scim

import (
	root "formal.local/w27-4e139ba19e"
)

// ---- 类型别名 ----

type (
	AttrExpr       = root.AttrExpr
	AttrPath       = root.AttrPath
	AttrSpec       = root.AttrSpec
	Caller         = root.Caller
	Engine         = root.Engine
	ErrorResponse  = root.ErrorResponse
	Expr           = root.Expr
	LexError       = root.LexError
	Literal        = root.Literal
	LiteralKind    = root.LiteralKind
	LogicalExpr    = root.LogicalExpr
	LogicalOp      = root.LogicalOp
	Mutability     = root.Mutability
	NotExpr        = root.NotExpr
	Operator       = root.Operator
	Option         = root.Option
	Parser         = root.Parser
	PatchOperation = root.PatchOperation
	PatchPath      = root.PatchPath
	PatchRequest   = root.PatchRequest
	Policy         = root.Policy
	Resource       = root.Resource
	ResourceSchema = root.ResourceSchema
	Schema         = root.Schema
	Store          = root.Store
	Tx             = root.Tx
	ValuePathExpr  = root.ValuePathExpr
)

// ---- 解析器 / AST 常量 ----

const (
	DefaultMaxASTDepth     = root.DefaultMaxASTDepth
	DefaultMaxNodes        = root.DefaultMaxNodes
	DefaultMaxFilterLength = root.DefaultMaxFilterLength
)

const (
	OpEq = root.OpEq
	OpNe = root.OpNe
	OpCo = root.OpCo
	OpSw = root.OpSw
	OpEw = root.OpEw
	OpPr = root.OpPr
	OpGt = root.OpGt
	OpGe = root.OpGe
	OpLt = root.OpLt
	OpLe = root.OpLe

	LogicalAnd = root.LogicalAnd
	LogicalOr  = root.LogicalOr

	LitString = root.LitString
	LitNumber = root.LitNumber
	LitBool   = root.LitBool
	LitNull   = root.LitNull

	MutReadWrite = root.MutReadWrite
	MutReadOnly  = root.MutReadOnly
	MutImmutable = root.MutImmutable
	MutWriteOnly = root.MutWriteOnly
)

// ---- 哨兵错误（保持 errors.Is 同一性）----

var (
	ErrInvalidFilter       = root.ErrInvalidFilter
	ErrFilterDepth         = root.ErrFilterDepth
	ErrFilterComplexity    = root.ErrFilterComplexity
	ErrInvalidPatch        = root.ErrInvalidPatch
	ErrInvalidPatchPath    = root.ErrInvalidPatchPath
	ErrSensitiveAttribute  = root.ErrSensitiveAttribute
	ErrProtectedAttribute  = root.ErrProtectedAttribute
	ErrTenantScopeRequired = root.ErrTenantScopeRequired
	ErrReadOnlyAttribute   = root.ErrReadOnlyAttribute
	ErrImmutableAttribute  = root.ErrImmutableAttribute
	ErrUnknownAttribute    = root.ErrUnknownAttribute
	ErrPrivilegedAttribute = root.ErrPrivilegedAttribute
	ErrPrivilegedGroup     = root.ErrPrivilegedGroup
	ErrCrossTenantMember   = root.ErrCrossTenantMember
	ErrTenantMismatch      = root.ErrTenantMismatch
	ErrNotFound            = root.ErrNotFound
)

// ---- 构造与解析函数 ----

var (
	Parse              = root.Parse
	ParseFilter        = root.ParseFilter
	NewParser          = root.NewParser
	ParsePatchPath     = root.ParsePatchPath
	ParseAttrPath      = root.ParseAttrPath
	StringLiteral      = root.StringLiteral
	QuoteValue         = root.QuoteValue
	WithMaxASTDepth    = root.WithMaxASTDepth
	WithMaxNodes       = root.WithMaxNodes
	WithMaxInputLength = root.WithMaxInputLength

	Walk       = root.Walk
	CountNodes = root.CountNodes

	Match           = root.Match
	BindTenantScope = root.BindTenantScope
	TenantPredicate = root.TenantPredicate
	CloneResource   = root.CloneResource

	DefaultPolicy       = root.DefaultPolicy
	NewPolicy           = root.NewPolicy
	AttributeMatchesRef = root.AttributeMatchesRef

	DefaultSchema     = root.DefaultSchema
	NewSchema         = root.NewSchema
	NewResourceSchema = root.NewResourceSchema

	NewEngine = root.NewEngine

	NewStore           = root.NewStore
	NormalizeReference = root.NormalizeReference

	HTTPStatus       = root.HTTPStatus
	NewErrorResponse = root.NewErrorResponse
)
