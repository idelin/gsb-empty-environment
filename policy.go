package scim

import (
	"fmt"
	"strings"
)

// Caller 描述经过宿主认证后的调用方上下文。
type Caller struct {
	// TenantID 是调用方所属租户；平台管理员可为空。
	TenantID string
	// SubjectID 是调用方用户 ID。
	SubjectID string
	// SystemAdmin 表示平台/系统管理员，可越过租户资源边界与特权组边界，
	// 但默认仍不能建立跨租户成员关系。
	SystemAdmin bool
	// TenantAdmin 表示租户管理员（在本模型中仍属于低权限主体）。
	TenantAdmin bool
	// Roles 是额外的应用角色，例如 identity-auditor / identity-admin。
	Roles []string
}

// HasRole 大小写不敏感地检查角色。
func (c Caller) HasRole(role string) bool {
	for _, r := range c.Roles {
		if strings.EqualFold(r, role) {
			return true
		}
	}
	return false
}

// Policy 是租户隔离与属性授权策略集合。零值不可直接使用，请用 DefaultPolicy
// 或 NewPolicy 构造后按需调整。
type Policy struct {
	// TenantAttribute 是资源上的隐式租户外键，默认 tenantId。
	TenantAttribute string
	// MaxFilterDepth 是 filter / PATCH path 子过滤器的最大 AST 深度。
	MaxFilterDepth int

	// SensitiveAttributes 是禁止未授权调用方查询的属性引用
	// （支持 roles、name.familyName、完整 URN 三种写法）。
	SensitiveAttributes []string
	// SensitiveReaderRoles 是允许读取敏感属性的角色；SystemAdmin 恒允许。
	SensitiveReaderRoles map[string]bool

	// PrivilegedAttributes 是只有特权角色才能 PATCH 的属性，
	// 例如 roles/active/externalId。
	PrivilegedAttributes []string
	// PrivilegedAttributeRoles 是允许修改特权属性的角色；SystemAdmin 恒允许。
	PrivilegedAttributeRoles map[string]bool

	// PrivilegedGroupIDs 按组 ID 标记特权组（如系统管理员组）。
	PrivilegedGroupIDs map[string]bool
	// PrivilegedGroupDisplayNames 按 displayName（大小写不敏感）标记特权组。
	PrivilegedGroupDisplayNames map[string]bool
	// GroupAdministratorRoles 是允许修改特权组的角色；SystemAdmin 恒允许。
	GroupAdministratorRoles map[string]bool

	// AllowCrossTenantMembers 关闭跨租户成员挂载的硬边界（极度危险，默认 false）。
	AllowCrossTenantMembers bool
	// AllowUnknownPatchAttributes 允许 PATCH schema 未登记属性。
	// 默认 false 以阻止 mass assignment。
	AllowUnknownPatchAttributes bool
}

// DefaultPolicy 返回安全默认策略：
//   - 租户键 tenantId，AST 深度上限 DefaultMaxASTDepth；
//   - roles/groups/externalId 为敏感查询属性；
//   - roles/active/externalId 为特权修改属性，需 identity-admin 或系统管理员；
//   - 跨租户成员引用一律拒绝；
//   - 未登记 schema 的 PATCH 属性一律拒绝。
func DefaultPolicy() *Policy {
	return &Policy{
		TenantAttribute:             "tenantId",
		MaxFilterDepth:              DefaultMaxASTDepth,
		SensitiveAttributes:         []string{"roles", "groups", "externalId"},
		SensitiveReaderRoles:        stringSet("identity-auditor", "identity-admin"),
		PrivilegedAttributes:        []string{"roles", "active", "externalId"},
		PrivilegedAttributeRoles:    stringSet("identity-admin"),
		PrivilegedGroupIDs:          map[string]bool{},
		PrivilegedGroupDisplayNames: stringSet("system administrators", "system administrator", "sysadmins"),
		GroupAdministratorRoles:     stringSet("group-admin"),
		AllowCrossTenantMembers:     false,
		AllowUnknownPatchAttributes: false,
	}
}

// NewPolicy 是 DefaultPolicy 的别名，便于宿主显式构造。
func NewPolicy() *Policy { return DefaultPolicy() }

func stringSet(items ...string) map[string]bool {
	m := make(map[string]bool, len(items))
	for _, it := range items {
		m[strings.ToLower(it)] = true
	}
	return m
}

// WithSensitiveAttributes 追加敏感查询属性并返回策略自身。
func (p *Policy) WithSensitiveAttributes(refs ...string) *Policy {
	p.SensitiveAttributes = append(p.SensitiveAttributes, refs...)
	return p
}

// WithPrivilegedAttributes 追加特权修改属性。
func (p *Policy) WithPrivilegedAttributes(refs ...string) *Policy {
	p.PrivilegedAttributes = append(p.PrivilegedAttributes, refs...)
	return p
}

// WithPrivilegedGroupIDs 登记特权组 ID。
func (p *Policy) WithPrivilegedGroupIDs(ids ...string) *Policy {
	if p.PrivilegedGroupIDs == nil {
		p.PrivilegedGroupIDs = map[string]bool{}
	}
	for _, id := range ids {
		p.PrivilegedGroupIDs[id] = true
	}
	return p
}

// WithPrivilegedGroupNames 登记特权组 displayName。
func (p *Policy) WithPrivilegedGroupNames(names ...string) *Policy {
	if p.PrivilegedGroupDisplayNames == nil {
		p.PrivilegedGroupDisplayNames = map[string]bool{}
	}
	for _, n := range names {
		p.PrivilegedGroupDisplayNames[strings.ToLower(n)] = true
	}
	return p
}

func (p *Policy) tenantAttribute() string {
	if p.TenantAttribute == "" {
		return "tenantId"
	}
	return p.TenantAttribute
}

func (p *Policy) maxDepth() int {
	if p.MaxFilterDepth <= 0 {
		return DefaultMaxASTDepth
	}
	return p.MaxFilterDepth
}

// AttributeMatchesRef 判断属性路径是否匹配策略引用。
// 单段引用（roles）匹配该根属性及其任意子属性；多段引用按完整路径匹配。
func AttributeMatchesRef(ref string, path AttrPath) bool {
	if strings.EqualFold(ref, path.String()) ||
		strings.EqualFold(ref, path.Canonical()) ||
		strings.EqualFold(ref, path.FullCanonical()) {
		return true
	}
	refPath, err := ParseAttrPath(ref)
	if err != nil {
		return false
	}
	if refPath.Schema == "" && len(refPath.Parts) == 1 {
		return strings.EqualFold(refPath.Parts[0], path.Root())
	}
	if !strings.EqualFold(refPath.Schema, path.Schema) {
		return false
	}
	if len(refPath.Parts) != len(path.Parts) {
		return false
	}
	for i := range refPath.Parts {
		if !strings.EqualFold(refPath.Parts[i], path.Parts[i]) {
			return false
		}
	}
	return true
}

func (p *Policy) pathInList(path AttrPath, refs []string) bool {
	for _, ref := range refs {
		if AttributeMatchesRef(ref, path) {
			return true
		}
	}
	return false
}

// IsSensitive 报告路径是否为敏感查询属性（含租户托管键）。
func (p *Policy) IsSensitive(path AttrPath) bool {
	if AttributeMatchesRef(p.tenantAttribute(), path) {
		return true
	}
	return p.pathInList(path, p.SensitiveAttributes)
}

// IsPrivilegedAttribute 报告路径是否为特权修改属性。
func (p *Policy) IsPrivilegedAttribute(path AttrPath) bool {
	return p.pathInList(path, p.PrivilegedAttributes)
}

// CanReadSensitive 判断调用方是否可读取敏感属性。
func (p *Policy) CanReadSensitive(c Caller) bool {
	if c.SystemAdmin {
		return true
	}
	for role := range p.SensitiveReaderRoles {
		if c.HasRole(role) {
			return true
		}
	}
	return false
}

// CanModifyPrivilegedAttributes 判断调用方是否可修改特权属性。
func (p *Policy) CanModifyPrivilegedAttributes(c Caller) bool {
	if c.SystemAdmin {
		return true
	}
	for role := range p.PrivilegedAttributeRoles {
		if c.HasRole(role) {
			return true
		}
	}
	return false
}

// CanModifyPrivilegedGroup 判断调用方是否可改动特权组。
func (p *Policy) CanModifyPrivilegedGroup(c Caller) bool {
	if c.SystemAdmin {
		return true
	}
	for role := range p.GroupAdministratorRoles {
		if c.HasRole(role) {
			return true
		}
	}
	return false
}

// IsPrivilegedGroup 综合资源标志位、组 ID 与 displayName 判断特权组。
func (p *Policy) IsPrivilegedGroup(group Resource) bool {
	if group == nil {
		return false
	}
	if flagged, ok := group["privileged"].(bool); ok && flagged {
		return true
	}
	if id, ok := group["id"].(string); ok && p.PrivilegedGroupIDs != nil && p.PrivilegedGroupIDs[id] {
		return true
	}
	if name, ok := group["displayName"].(string); ok {
		if p.PrivilegedGroupDisplayNames != nil &&
			p.PrivilegedGroupDisplayNames[strings.ToLower(name)] {
			return true
		}
	}
	return false
}

// AuthorizeFilter 遍历 AST，拒绝未授权的敏感属性引用。
// 值路径 attr[...] 的外层属性与其内部子过滤器都会被检查。
func (e *Engine) AuthorizeFilter(c Caller, expr Expr) error {
	p := DefaultPolicy()
	if e != nil && e.Policy != nil {
		p = e.Policy
	}
	denied := ""
	Walk(expr, func(node Expr) {
		var path *AttrPath
		switch n := node.(type) {
		case *AttrExpr:
			path = &n.Path
		case *ValuePathExpr:
			path = &n.Path
		}
		if path == nil || denied != "" {
			return
		}
		// 租户键由服务端独占绑定，外部 filter 不得直接探测。
		if AttributeMatchesRef(p.tenantAttribute(), *path) {
			denied = path.String()
			return
		}
		if p.IsSensitive(*path) && !p.CanReadSensitive(c) {
			denied = path.String()
		}
	})
	if denied != "" {
		if AttributeMatchesRef(p.tenantAttribute(), mustParsePath(denied)) {
			return fmt.Errorf("%w: %s", ErrProtectedAttribute, denied)
		}
		return fmt.Errorf("%w: %s requires an elevated reader role",
			ErrSensitiveAttribute, denied)
	}
	return nil
}

func mustParsePath(raw string) AttrPath {
	ap, _ := ParseAttrPath(raw)
	return ap
}

// SecureFilter 是宿主查询入口的一站式方法：按策略深度解析、做敏感属性
// 授权检查，并绑定调用方的隐式租户隔离谓词。
func (e *Engine) SecureFilter(c Caller, rawFilter string) (Expr, error) {
	p := e.policyOrDefault()
	var expr Expr
	if strings.TrimSpace(rawFilter) != "" {
		parsed, err := ParseFilter(rawFilter, WithMaxASTDepth(p.maxDepth()))
		if err != nil {
			return nil, err
		}
		expr = parsed
	}
	if err := e.AuthorizeFilter(c, expr); err != nil {
		return nil, err
	}
	tenantID := c.TenantID
	if tenantID == "" {
		if c.SystemAdmin {
			// 平台管理员且未指定租户：不绑定租户谓词。
			return expr, nil
		}
		return nil, ErrTenantScopeRequired
	}
	return BindTenantScope(expr, p.tenantAttribute(), tenantID), nil
}
