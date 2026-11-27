package scim

import (
	"context"
	"fmt"
	"strings"
)

// Engine 把解析器、策略、schema 与存储组装为身份同步宿主可直接调用的门面。
type Engine struct {
	Schema *Schema
	Policy *Policy
	Store  *Store
}

// NewEngine 创建带默认 schema / 策略 / 内存存储的引擎。
func NewEngine() *Engine {
	return &Engine{
		Schema: DefaultSchema(),
		Policy: DefaultPolicy(),
		Store:  NewStore(),
	}
}

// WithStore 注入自定义存储并返回引擎自身。
func (e *Engine) WithStore(s *Store) *Engine {
	e.Store = s
	return e
}

// WithPolicy 注入自定义策略。
func (e *Engine) WithPolicy(p *Policy) *Engine {
	e.Policy = p
	return e
}

func (e *Engine) policyOrDefault() *Policy {
	if e.Policy == nil {
		e.Policy = DefaultPolicy()
	}
	return e.Policy
}

func (e *Engine) schemaOrDefault() *Schema {
	if e.Schema == nil {
		e.Schema = DefaultSchema()
	}
	return e.Schema
}

func (e *Engine) tenantAttribute() string {
	return e.policyOrDefault().tenantAttribute()
}

func (e *Engine) tenantOf(r Resource) string {
	if r == nil {
		return ""
	}
	s, _ := r[e.tenantAttribute()].(string)
	return s
}

// List 解析并授权 filter、绑定租户谓词，然后返回匹配资源的深拷贝。
// rawFilter 为空时仅返回调用方租户内全部资源。
func (e *Engine) List(ctx context.Context, c Caller, resourceType, rawFilter string) ([]Resource, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	secured, err := e.SecureFilter(c, rawFilter)
	if err != nil {
		return nil, err
	}

	var out []Resource
	err = e.Store.View(func(tx *Tx) error {
		for _, r := range tx.resources(resourceType) {
			if err := ctx.Err(); err != nil {
				return err
			}
			ok, merr := Match(secured, r)
			if merr != nil {
				return merr
			}
			if ok {
				out = append(out, CloneResource(r))
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// Get 按 ID 读取单个资源，并强制执行租户边界。
func (e *Engine) Get(ctx context.Context, c Caller, resourceType, id string) (Resource, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	r, ok := e.Store.Get(resourceType, id)
	if !ok {
		return nil, fmt.Errorf("%w: %s/%s", ErrNotFound, resourceType, id)
	}
	if err := e.checkTargetTenant(c, r); err != nil {
		return nil, err
	}
	return r, nil
}

// Patch 是 ApplyPatch 的便捷可变参数形式。
func (e *Engine) Patch(ctx context.Context, c Caller, resourceType, id string, ops ...PatchOperation) (Resource, error) {
	return e.ApplyPatch(ctx, c, resourceType, id, PatchRequest{Operations: ops})
}

// ApplyPatch 在单个快照事务中执行 RFC 7644 PATCH：
// 任一操作的路径解析、可变性/授权/跨租户校验或应用失败，整个事务回滚，
// 存储恢复到 PATCH 前状态。
func (e *Engine) ApplyPatch(ctx context.Context, c Caller, resourceType, id string, req PatchRequest) (Resource, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(req.Operations) == 0 {
		return nil, fmt.Errorf("%w: Operations must not be empty", ErrInvalidPatch)
	}
	policy := e.policyOrDefault()

	var updated Resource
	err := e.Store.Transact(func(tx *Tx) error {
		current, ok := tx.Get(resourceType, id)
		if !ok {
			return fmt.Errorf("%w: %s/%s", ErrNotFound, resourceType, id)
		}
		if err := e.checkTargetTenant(c, current); err != nil {
			return err
		}

		for i := range req.Operations {
			if err := ctx.Err(); err != nil {
				return err
			}
			op := req.Operations[i]
			op.Op = strings.ToLower(strings.TrimSpace(op.Op))
			switch op.Op {
			case "add", "remove", "replace":
			default:
				return fmt.Errorf("%w: operation %d has unsupported op %q",
					ErrInvalidPatch, i, req.Operations[i].Op)
			}

			pp, perr := ParsePatchPath(op.Path, WithMaxASTDepth(policy.maxDepth()))
			if perr != nil {
				return perr
			}
			if verr := e.validatePatchOp(c, resourceType, current, op, pp, tx); verr != nil {
				return verr
			}
			if aerr := applyPatchOperation(current, op, pp); aerr != nil {
				return fmt.Errorf("%w: operation %d (%s %q): %v",
					ErrInvalidPatch, i, op.Op, op.Path, aerr)
			}
		}
		updated = CloneResource(current)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return updated, nil
}

func (tx *Tx) resources(resourceType string) []Resource {
	coll := tx.store.collections[resourceType]
	out := make([]Resource, 0, len(coll))
	for _, r := range coll {
		out = append(out, r)
	}
	return out
}

// checkTargetTenant 阻止跨租户读写目标资源；跨租户 ID 统一按 NotFound 处理，
// 避免通过错误差异探测其它租户的资源是否存在。
func (e *Engine) checkTargetTenant(c Caller, target Resource) error {
	targetTenant := e.tenantOf(target)
	if c.SystemAdmin && strings.TrimSpace(c.TenantID) == "" {
		return nil
	}
	if targetTenant != c.TenantID {
		return fmt.Errorf("%w: %w: caller tenant %q, resource tenant %q",
			ErrNotFound, ErrTenantMismatch, c.TenantID, targetTenant)
	}
	return nil
}

// normalizeResourceType 把 Users/Groups 端点形式归一为 User/Group schema 名。
func normalizeResourceType(resourceType string) string {
	switch resourceType {
	case "Users", "users":
		return "User"
	case "Groups", "groups":
		return "Group"
	}
	return strings.TrimSuffix(resourceType, "s")
}

func (e *Engine) validatePatchOp(
	c Caller,
	resourceType string,
	target Resource,
	op PatchOperation,
	pp *PatchPath,
	tx *Tx,
) error {
	policy := e.policyOrDefault()
	schema := e.schemaOrDefault()
	rt := normalizeResourceType(resourceType)

	// 收集本次实际写入的属性路径：空 path 时展开 value 对象的每个键。
	var paths []AttrPath
	if pp.IsRoot() {
		if op.Op == "remove" {
			return fmt.Errorf("%w: remove without path is not allowed", ErrInvalidPatchPath)
		}
		values, ok := op.Value.(map[string]any)
		if !ok {
			return fmt.Errorf("%w: add/replace with empty path requires an object value",
				ErrInvalidPatch)
		}
		for key := range values {
			ap, err := ParseAttrPath(key)
			if err != nil {
				return fmt.Errorf("%w: attribute name %q: %v", ErrInvalidPatchPath, key, err)
			}
			paths = append(paths, ap)
		}
	} else {
		paths = append(paths, pp.MutationPath())
	}

	rs, schemaKnown := schema.Resource(rt)
	for _, ap := range paths {
		if schemaKnown {
			spec, known := rs.Lookup(ap.Root())
			if known {
				switch spec.Mutability {
				case MutReadOnly:
					return fmt.Errorf("%w: %s", ErrReadOnlyAttribute, ap.Root())
				case MutImmutable:
					return fmt.Errorf("%w: %s", ErrImmutableAttribute, ap.Root())
				}
			} else if !policy.AllowUnknownPatchAttributes {
				return fmt.Errorf("%w: %s is not declared in %s schema",
					ErrUnknownAttribute, ap.Root(), rt)
			}
		}

		if policy.IsPrivilegedAttribute(ap) && !policy.CanModifyPrivilegedAttributes(c) {
			return fmt.Errorf("%w: %s requires identity-admin role",
				ErrPrivilegedAttribute, ap.Root())
		}
	}

	if rt == "Group" {
		// 特权组（如系统管理员组）的任何写操作都需要组管理员权限。
		if policy.IsPrivilegedGroup(target) && !policy.CanModifyPrivilegedGroup(c) {
			name, _ := target["displayName"].(string)
			id, _ := target["id"].(string)
			return fmt.Errorf("%w: %s (%s) cannot be modified by caller %q",
				ErrPrivilegedGroup, name, id, c.SubjectID)
		}
		// PATCH path 中的 attr[filter] 子过滤器同样不得探测敏感属性。
		if pp.Filter != nil {
			if err := e.AuthorizeFilter(c, pp.Filter); err != nil {
				return err
			}
		}

		switch {
		case !pp.IsRoot() && strings.EqualFold(pp.Attr.Root(), "members"):
			if err := e.validateMemberRefs(target, mountedMemberRefs(op, pp), tx); err != nil {
				return err
			}
		case pp.IsRoot():
			// 空 path 整体替换时若携带 members 也要校验。
			if vm, ok := op.Value.(map[string]any); ok {
				if members, has := vm["members"]; has {
					if err := e.validateMemberRefs(target, extractMemberRefs(members), tx); err != nil {
						return err
					}
				}
			}
		}
	}
	return nil
}

// mountedMemberRefs 仅提取本次 PATCH 真正新挂载的成员引用，避免把
// members[...].displayName 等子属性的标量值误判为跨租户 ID。
func mountedMemberRefs(op PatchOperation, pp *PatchPath) []string {
	if pp == nil || pp.IsRoot() || !strings.EqualFold(pp.Attr.Root(), "members") {
		return nil
	}
	if len(pp.SubPath) > 0 {
		leaf := pp.SubPath[len(pp.SubPath)-1]
		if strings.EqualFold(leaf, "value") {
			return extractMemberRefs(op.Value)
		}
		return nil
	}
	return extractMemberRefs(op.Value)
}

// validateMemberRefs 实现跨租户成员引用硬边界。特权组授权已在调用方完成。
func (e *Engine) validateMemberRefs(group Resource, refs []string, tx *Tx) error {
	policy := e.policyOrDefault()

	groupTenant := e.tenantOf(group)
	for _, rawRef := range refs {
		id := NormalizeReference(rawRef)
		member, _, ok := tx.Lookup(id)
		if !ok {
			return fmt.Errorf("%w: member reference %q does not exist", ErrNotFound, rawRef)
		}
		memberTenant := e.tenantOf(member)
		if !policy.AllowCrossTenantMembers && memberTenant != groupTenant {
			return fmt.Errorf(
				"%w: member %q is in tenant %q but group %q is in tenant %q",
				ErrCrossTenantMember, id, memberTenant,
				group["id"], groupTenant)
		}
	}
	return nil
}
