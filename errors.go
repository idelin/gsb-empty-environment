package scim

import (
	"errors"
	"fmt"
)

// 安全策略与解析阶段暴露的哨兵错误。宿主可通过 errors.Is 映射为
// RFC 7644 规定的 HTTP 状态码与 scimType。
var (
	// ErrInvalidFilter 表示 filter 表达式不符合 RFC 7644 语法。
	ErrInvalidFilter = errors.New("invalid SCIM filter")
	// ErrFilterDepth 表示 filter / 值路径嵌套深度超过限制。
	ErrFilterDepth = errors.New("SCIM filter exceeds maximum AST depth")
	// ErrFilterComplexity 表示输入长度或 AST 节点数超过限制。
	ErrFilterComplexity = errors.New("SCIM filter exceeds maximum complexity")

	// ErrInvalidPatch 表示 PATCH 请求本身不合法（op 未知等）。
	ErrInvalidPatch = errors.New("invalid SCIM PATCH operation")
	// ErrInvalidPatchPath 表示 PATCH path 表达式不符合语法。
	ErrInvalidPatchPath = errors.New("invalid SCIM PATCH path")

	// ErrSensitiveAttribute 表示调用方尝试查询未授权的敏感属性。
	ErrSensitiveAttribute = errors.New("query of sensitive attribute is not authorized")
	// ErrProtectedAttribute 表示外部输入引用了由服务端托管的保护属性（如租户隔离键）。
	ErrProtectedAttribute = errors.New("attribute is server-managed and cannot be queried directly")
	// ErrTenantScopeRequired 表示无法为调用方绑定隐式租户谓词。
	ErrTenantScopeRequired = errors.New("tenant isolation scope is required")

	// ErrReadOnlyAttribute 表示 PATCH 命中 readOnly 属性。
	ErrReadOnlyAttribute = errors.New("attribute is readOnly and cannot be modified")
	// ErrImmutableAttribute 表示 PATCH 命中 immutable 属性。
	ErrImmutableAttribute = errors.New("attribute is immutable and cannot be modified")
	// ErrUnknownAttribute 表示严格模式下 PATCH 了 schema 未声明的属性。
	ErrUnknownAttribute = errors.New("attribute is not declared in schema")
	// ErrPrivilegedAttribute 表示调用方无权修改 roles/active/externalId 等特权属性。
	ErrPrivilegedAttribute = errors.New("modification of privileged attribute is not authorized")

	// ErrPrivilegedGroup 表示低权限调用方试图改动特权组（如系统管理员组）。
	ErrPrivilegedGroup = errors.New("modification of privileged group membership is not authorized")
	// ErrCrossTenantMember 表示试图挂载其它租户的成员引用。
	ErrCrossTenantMember = errors.New("cross-tenant member reference is forbidden")

	// ErrTenantMismatch 表示目标资源不属于调用方租户。
	ErrTenantMismatch = errors.New("resource belongs to a different tenant")
	// ErrNotFound 表示资源或成员引用不存在（跨租户访问也统一返回该错误，避免探测）。
	ErrNotFound = errors.New("SCIM resource not found")
)

// LexError 描述词法错误的字节位置。
type LexError struct {
	Pos int
	Msg string
}

func (e *LexError) Error() string {
	return fmt.Sprintf("lexical error at byte %d: %s", e.Pos, e.Msg)
}

// HTTPStatus 将策略/解析错误映射为建议的 HTTP 状态码。
func HTTPStatus(err error) int {
	switch {
	case err == nil:
		return 200
	case errors.Is(err, ErrNotFound):
		return 404
	case errors.Is(err, ErrSensitiveAttribute),
		errors.Is(err, ErrProtectedAttribute),
		errors.Is(err, ErrPrivilegedAttribute),
		errors.Is(err, ErrPrivilegedGroup),
		errors.Is(err, ErrCrossTenantMember),
		errors.Is(err, ErrTenantMismatch):
		return 403
	case errors.Is(err, ErrInvalidFilter),
		errors.Is(err, ErrFilterDepth),
		errors.Is(err, ErrFilterComplexity),
		errors.Is(err, ErrInvalidPatch),
		errors.Is(err, ErrInvalidPatchPath),
		errors.Is(err, ErrReadOnlyAttribute),
		errors.Is(err, ErrImmutableAttribute),
		errors.Is(err, ErrUnknownAttribute):
		return 400
	case errors.Is(err, ErrTenantScopeRequired):
		return 401
	default:
		return 500
	}
}

// ErrorResponse 是可直接 JSON 序列化的 RFC 7644 §3.12 错误响应体。
type ErrorResponse struct {
	Schemas  []string `json:"schemas"`
	Status   string   `json:"status"`
	SCIMType string   `json:"scimType,omitempty"`
	Detail   string   `json:"detail,omitempty"`
}

// NewErrorResponse 把内部错误转换为 SCIM 错误响应。
func NewErrorResponse(err error) ErrorResponse {
	status := HTTPStatus(err)
	scimType := ""
	switch {
	case errors.Is(err, ErrReadOnlyAttribute), errors.Is(err, ErrImmutableAttribute):
		scimType = "mutability"
	case errors.Is(err, ErrInvalidFilter), errors.Is(err, ErrInvalidPatchPath):
		scimType = "invalidFilter"
	case errors.Is(err, ErrInvalidPatch):
		scimType = "invalidSyntax"
	case errors.Is(err, ErrCrossTenantMember), errors.Is(err, ErrPrivilegedGroup),
		errors.Is(err, ErrSensitiveAttribute), errors.Is(err, ErrPrivilegedAttribute):
		scimType = "forbidden"
	}
	return ErrorResponse{
		Schemas:  []string{"urn:ietf:params:scim:api:messages:2.0:Error"},
		Status:   fmt.Sprintf("%d", status),
		SCIMType: scimType,
		Detail:   detail(err),
	}
}

func detail(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
