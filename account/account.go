// Package account 演示账户服务如何声明字段策略并调用 binder 组件。
package account

import (
	"example.org/netsec/q0292/binder"
)

// User 账户模型。JSON 键名即字段策略中使用的名字。
type User struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Email    string `json:"email"`
	Bio      string `json:"bio"`
	Avatar   string `json:"avatar"`
	Verified bool   `json:"verified"`

	// 以下字段只能由服务端流程写入，客户端绑定永不触及。
	Role       string `json:"role"`
	TenantID   string `json:"tenant_id"`
	Quota      int    `json:"quota"`
	AuditTrail string `json:"audit_trail"`
}

// ProfileSchema 资料更新接口的字段策略。
var ProfileSchema = binder.NewSchema(
	"profile.update",
	/* writable */ []string{"name", "email", "bio", "avatar"},
	/* readOnly */ []string{"id", "verified"},
	/* serverOnly */ []string{"role", "tenant_id", "quota", "audit_trail"},
)

// Service 账户服务。
type Service struct {
	auth binder.Authorizer
}

// NewService 构造服务；auth 为服务端字段变更的独立授权判定。
func NewService(auth binder.Authorizer) *Service {
	return &Service{auth: auth}
}

// UpdateProfile 处理客户端资料更新请求。
// 只应用白名单字段，返回绑定结果供调用方审计拒绝项。
func (s *Service) UpdateProfile(u *User, body []byte) (*binder.Result, error) {
	return ProfileSchema.BindClient(body, u)
}

// AdminSetFields 服务端流程（角色变更、租户迁移、额度调整、审计追加）
// 写入服务器字段的唯一入口，必须携带独立授权。
func (s *Service) AdminSetFields(actor string, u *User, body []byte) (*binder.Result, error) {
	return ProfileSchema.ApplyServer(actor, body, u, s.auth)
}
