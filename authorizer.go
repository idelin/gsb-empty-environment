package netsec

import (
	"errors"
	"fmt"
	"regexp"
)

var (
	// ErrOperationNotDeclared 表示操作未在角色矩阵中声明，默认拒绝。
	ErrOperationNotDeclared = errors.New("netsec: operation not declared")
	// ErrRoleNotGranted 表示调用者不具备操作所需的任何角色。
	ErrRoleNotGranted = errors.New("netsec: required role not granted")
	// ErrScopeNotAllowed 表示请求的资源范围超出操作声明的范围。
	ErrScopeNotAllowed = errors.New("netsec: resource scope not allowed")
	// ErrExtensionNotAllowed 表示请求携带未声明或取值非法的扩展元素。
	ErrExtensionNotAllowed = errors.New("netsec: extension element not allowed")
	// ErrMalformedRequest 表示请求标识字段缺失或格式非法。
	ErrMalformedRequest = errors.New("netsec: malformed request")
)

// identPattern 约束命名空间、操作名与版本号，拒绝空串、通配符与空白字符。
var identPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

// OperationSpec 描述一个已声明操作的授权策略。
type OperationSpec struct {
	Namespace  string
	Name       string
	Version    string
	Roles      []string          // 任一命中即放行
	Scopes     []string          // 允许的资源范围
	Extensions map[string]string // 允许的扩展键及其限定取值，空值表示任意取值
}

// Request 是网关转发的一次操作调用。
type Request struct {
	Namespace  string
	Operation  string
	Version    string
	Roles      []string
	Scope      string
	Extensions map[string]string
}

// Authorizer 按显式声明的操作矩阵做默认拒绝授权。
type Authorizer struct {
	ops map[operationKey]OperationSpec
}

type operationKey struct {
	namespace string
	name      string
	version   string
}

// NewAuthorizer 构建授权组件；声明中标识非法的条目会被忽略，等同于未声明。
func NewAuthorizer(specs []OperationSpec) *Authorizer {
	ops := make(map[operationKey]OperationSpec, len(specs))
	for _, spec := range specs {
		if !validIdent(spec.Namespace) || !validIdent(spec.Name) || !validIdent(spec.Version) {
			continue
		}
		ops[operationKey{spec.Namespace, spec.Name, spec.Version}] = spec
	}
	return &Authorizer{ops: ops}
}

// Authorize 对单次调用授权，任何一项校验失败即拒绝。
func (a *Authorizer) Authorize(req Request) error {
	if !validIdent(req.Namespace) || !validIdent(req.Operation) || !validIdent(req.Version) {
		return fmt.Errorf("%w: invalid namespace/operation/version", ErrMalformedRequest)
	}

	spec, ok := a.ops[operationKey{req.Namespace, req.Operation, req.Version}]
	if !ok {
		return fmt.Errorf("%w: %s/%s@%s", ErrOperationNotDeclared, req.Namespace, req.Operation, req.Version)
	}

	if !hasAnyRole(req.Roles, spec.Roles) {
		return fmt.Errorf("%w: %s/%s@%s", ErrRoleNotGranted, req.Namespace, req.Operation, req.Version)
	}

	if !contains(spec.Scopes, req.Scope) {
		return fmt.Errorf("%w: scope %q", ErrScopeNotAllowed, req.Scope)
	}

	for key, value := range req.Extensions {
		allowed, declared := spec.Extensions[key]
		if !declared {
			return fmt.Errorf("%w: undeclared extension %q", ErrExtensionNotAllowed, key)
		}
		if allowed != "" && allowed != value {
			return fmt.Errorf("%w: extension %q value %q", ErrExtensionNotAllowed, key, value)
		}
	}

	return nil
}

// BatchResult 记录批量请求中单项的授权结果。
type BatchResult struct {
	Index   int
	Request Request
	Err     error
}

// AuthorizeBatch 对批量请求逐项独立授权，返回每一项的结果，互不放行。
func (a *Authorizer) AuthorizeBatch(reqs []Request) []BatchResult {
	results := make([]BatchResult, len(reqs))
	for i, req := range reqs {
		results[i] = BatchResult{Index: i, Request: req, Err: a.Authorize(req)}
	}
	return results
}

func validIdent(s string) bool {
	return identPattern.MatchString(s)
}

func hasAnyRole(granted, required []string) bool {
	for _, role := range granted {
		if contains(required, role) {
			return true
		}
	}
	return false
}

func contains(list []string, item string) bool {
	for _, v := range list {
		if v == item {
			return true
		}
	}
	return false
}
