package binder

import (
	"encoding/json"
	"errors"
	"fmt"
)

// Authorizer 独立授权接口：服务端流程在写服务器字段前必须提供。
// 实现方负责校验调用主体（服务账号、内部流程）是否被允许变更
// 指定字段，例如基于 RPC 身份、工单或审批记录。
type Authorizer interface {
	// AllowsServerField 报告主体 actor 是否可写服务器字段 field。
	AllowsServerField(actor, field string) bool
}

// AuthorizerFunc 让普通函数适配 Authorizer。
type AuthorizerFunc func(actor, field string) bool

// AllowsServerField 实现 Authorizer。
func (f AuthorizerFunc) AllowsServerField(actor, field string) bool {
	return f(actor, field)
}

// ErrUnauthorized 在缺少授权或授权拒绝服务器字段写入时返回。
var ErrUnauthorized = errors.New("binder: server field write not authorized")

// ApplyServer 在独立授权下把服务端流程产生的值写入 ServerOnly 字段。
// 这是角色、租户、额度与审计字段的唯一写入通道；客户端绑定永不触及。
// actor 标识发起变更的服务端主体，用于授权判定与审计日志。
// 任何未获授权的字段都会导致整体拒绝，不做部分写入。
func (s *Schema) ApplyServer(actor string, payload []byte, dst any, auth Authorizer) (*Result, error) {
	if auth == nil {
		return nil, fmt.Errorf("%w: nil authorizer", ErrUnauthorized)
	}
	obj, err := decodeObject(payload)
	if err != nil {
		return nil, err
	}
	index, err := fieldIndex(dst)
	if err != nil {
		return nil, err
	}
	serverOnly := make(map[string]bool, len(s.ServerOnly))
	for _, f := range s.ServerOnly {
		serverOnly[f] = true
	}

	// 先整体校验：全部字段必须属于 ServerOnly 且已获授权。
	for key := range obj {
		if !serverOnly[key] {
			s.logger.Warn("binder: server write rejected",
				"schema", s.Name, "actor", actor, "field", key, "reason", ReasonUnknownField)
			return nil, fmt.Errorf("binder: %q is not a server-only field of schema %q", key, s.Name)
		}
		if !auth.AllowsServerField(actor, key) {
			s.logger.Warn("binder: server write rejected",
				"schema", s.Name, "actor", actor, "field", key, "reason", ReasonNotAuthorized)
			return nil, fmt.Errorf("%w: actor %q field %q", ErrUnauthorized, actor, key)
		}
	}

	res := &Result{}
	for key, raw := range obj {
		fv, ok := index[key]
		if !ok {
			return nil, fmt.Errorf("binder: server-only field %q missing on target", key)
		}
		if err := assignValue(fv, json.RawMessage(raw)); err != nil {
			return nil, fmt.Errorf("binder: server field %q: %w", key, err)
		}
		res.ServerSet = append(res.ServerSet, key)
		s.logger.Info("binder: server field set",
			"schema", s.Name, "actor", actor, "field", key)
	}
	return res, nil
}
