package scim

import "strings"

// Mutability 对应 RFC 7643 §2.2.1 的属性可变性。
type Mutability string

const (
	MutReadWrite Mutability = "readWrite"
	MutReadOnly  Mutability = "readOnly"
	MutImmutable Mutability = "immutable"
	MutWriteOnly Mutability = "writeOnly"
)

// AttrSpec 描述单个属性的安全相关 schema 特征。
type AttrSpec struct {
	Name        string
	Mutability  Mutability
	MultiValued bool
}

// ResourceSchema 是某类资源（User/Group/...）的属性登记表。
type ResourceSchema struct {
	Name  string
	attrs map[string]*AttrSpec
}

// NewResourceSchema 创建资源 schema。
func NewResourceSchema(name string) *ResourceSchema {
	return &ResourceSchema{Name: name, attrs: map[string]*AttrSpec{}}
}

// Attr 以链式调用登记一个属性。
func (rs *ResourceSchema) Attr(name string, m Mutability, multiValued bool) *ResourceSchema {
	rs.attrs[strings.ToLower(name)] = &AttrSpec{
		Name:        name,
		Mutability:  m,
		MultiValued: multiValued,
	}
	return rs
}

// Lookup 按大小写不敏感方式查找属性定义。
func (rs *ResourceSchema) Lookup(name string) (*AttrSpec, bool) {
	if rs == nil {
		return nil, false
	}
	spec, ok := rs.attrs[strings.ToLower(name)]
	return spec, ok
}

// Schema 聚合所有资源类型的 schema。
type Schema struct {
	resources map[string]*ResourceSchema
}

// NewSchema 创建空 schema 集合。
func NewSchema() *Schema {
	return &Schema{resources: map[string]*ResourceSchema{}}
}

// Add 登记一个资源 schema。
func (s *Schema) Add(rs *ResourceSchema) *Schema {
	s.resources[rs.Name] = rs
	return s
}

// Resource 返回资源 schema。
func (s *Schema) Resource(name string) (*ResourceSchema, bool) {
	if rs, ok := s.resources[name]; ok {
		return rs, true
	}
	// 大小写不敏感回退，避免宿主传 user/users 等变体时绕过可变性校验。
	for key, rs := range s.resources {
		if strings.EqualFold(key, strings.TrimSuffix(name, "s")) || strings.EqualFold(key, name) {
			return rs, true
		}
	}
	return nil, false
}

// MutabilityOf 解析 PATCH 路径首段属性的可变性。
func (s *Schema) MutabilityOf(resourceType string, path AttrPath) (Mutability, bool) {
	rs, ok := s.resources[resourceType]
	if !ok {
		// 兼容调用方传入 Users/Groups 等集合名。
		rs, ok = s.resources[strings.TrimSuffix(resourceType, "s")]
		if !ok {
			return "", false
		}
	}
	spec, ok := rs.Lookup(path.Root())
	if !ok {
		return "", false
	}
	return spec.Mutability, true
}

// DefaultSchema 返回内置的 User/Group 安全 schema 子集。
//
// 除 RFC 7643 明确的 readOnly 属性（id/meta/groups）外，租户隔离键
// tenantId 被标记为 immutable，privileged 等服务端标志为 readOnly，
// 防止通过 PATCH 实施批量赋值（mass assignment）。
func DefaultSchema() *Schema {
	user := NewResourceSchema("User").
		Attr("id", MutReadOnly, false).
		Attr("externalId", MutReadWrite, false).
		Attr("meta", MutReadOnly, false).
		Attr("tenantId", MutImmutable, false).
		Attr("userName", MutReadWrite, false).
		Attr("password", MutWriteOnly, false).
		Attr("name", MutReadWrite, false).
		Attr("displayName", MutReadWrite, false).
		Attr("nickName", MutReadWrite, false).
		Attr("profileUrl", MutReadWrite, false).
		Attr("title", MutReadWrite, false).
		Attr("userType", MutReadWrite, false).
		Attr("preferredLanguage", MutReadWrite, false).
		Attr("locale", MutReadWrite, false).
		Attr("timezone", MutReadWrite, false).
		Attr("active", MutReadWrite, false).
		Attr("emails", MutReadWrite, true).
		Attr("phoneNumbers", MutReadWrite, true).
		Attr("ims", MutReadWrite, true).
		Attr("photos", MutReadWrite, true).
		Attr("addresses", MutReadWrite, true).
		Attr("entitlements", MutReadWrite, true).
		Attr("roles", MutReadWrite, true).
		Attr("x509Certificates", MutReadWrite, true).
		// RFC 7643: groups 由服务端计算，readOnly。
		Attr("groups", MutReadOnly, true)

	group := NewResourceSchema("Group").
		Attr("id", MutReadOnly, false).
		Attr("meta", MutReadOnly, false).
		Attr("tenantId", MutImmutable, false).
		Attr("displayName", MutReadWrite, false).
		Attr("members", MutReadWrite, true).
		// 服务端特权组标志，禁止 PATCH 私自提权。
		Attr("privileged", MutReadOnly, false)

	return NewSchema().Add(user).Add(group)
}
