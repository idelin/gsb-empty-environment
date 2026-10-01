// Package temporal 实现带双时态约束（业务时间 valid time + 知悉/审计时间
// transaction time）的 RBAC 权限委托与继承解析内核。
//
// 模型：
//   - 授权（Grant）：授予者将某角色授予被授予者，在业务时间区间
//     [ValidFrom, ValidTo) 内有效，并在知悉时间 RecordedAt 起对审计可见。
//     Grantor 为空串表示系统直接授权（委托链的根）。
//   - 撤回（Revocation）：在知悉时间 RecordedAt 被记录，使目标授权自业务
//     时间 ValidFrom 起失效（提前收回 / 追溯生效）。
//   - 查询：给定用户、业务时间 vt、审计时间 tt，求实际有效权限集合。
//     授权 g 在 (vt, tt) 下有效，当且仅当：
//     g.RecordedAt <= tt
//     g.ValidFrom <= vt < min(g.ValidTo, 所有 RecordedAt <= tt 的撤回的 ValidFrom)
//   - 委托链：非系统授权仅在授予者本人在 (vt, tt) 下实际持有该角色时
//     才有效（递归解析，带上层过期/收回的逐层传播与环保护）。
package temporal

import "sort"

// Infinity 表示无终止时间的业务时间上界。
const Infinity = int64(1)<<62 - 1

// Grant 表示一次角色授权或委托。
type Grant struct {
	ID         int64
	Grantor    string // 授予者；空串表示系统直接授权
	Grantee    string // 被授予者
	Role       string
	ValidFrom  int64 // 业务时间起点（含）
	ValidTo    int64 // 业务时间终点（不含）
	RecordedAt int64 // 知悉时间：该授权自何时起对审计可见
}

// Revocation 表示对某次授权的提前收回。
type Revocation struct {
	GrantID    int64
	ValidFrom  int64 // 自该业务时间起授权失效
	RecordedAt int64 // 知悉时间：撤回自何时起对审计可见
}

// Resolver 是时态权限解析内核。
type Resolver struct {
	nextID   int64
	grants   map[int64]*Grant
	revokes  map[int64][]Revocation // grantID -> 撤回列表
	rolePerm map[string]map[string]bool
}

// NewResolver 创建空的解析器。
func NewResolver() *Resolver {
	return &Resolver{
		grants:   make(map[int64]*Grant),
		revokes:  make(map[int64][]Revocation),
		rolePerm: make(map[string]map[string]bool),
	}
}

// GrantRole 记录一次授权/委托，返回授权 ID。
func (r *Resolver) GrantRole(g Grant) int64 {
	r.nextID++
	g.ID = r.nextID
	cp := g
	r.grants[g.ID] = &cp
	return g.ID
}

// Revoke 记录一次撤回：使授权 grantID 自业务时间 validFrom 起失效，
// 该事实在知悉时间 recordedAt 起对审计可见。
func (r *Resolver) Revoke(grantID, validFrom, recordedAt int64) {
	r.revokes[grantID] = append(r.revokes[grantID], Revocation{
		GrantID:    grantID,
		ValidFrom:  validFrom,
		RecordedAt: recordedAt,
	})
}

// SetRolePermissions 设置角色到权限集合的映射。
func (r *Resolver) SetRolePermissions(role string, perms ...string) {
	set := make(map[string]bool, len(perms))
	for _, p := range perms {
		set[p] = true
	}
	r.rolePerm[role] = set
}

// effectiveTo 返回授权在审计时间 tt 下的实际业务时间终点：
// 原始终点与所有已知撤回起点的最小值。
func (r *Resolver) effectiveTo(g *Grant, tt int64) int64 {
	end := g.ValidTo
	for _, rev := range r.revokes[g.ID] {
		if rev.RecordedAt <= tt && rev.ValidFrom < end {
			end = rev.ValidFrom
		}
	}
	return end
}

// grantActive 判断授权本身（不看委托链上游）在 (vt, tt) 下是否有效。
func (r *Resolver) grantActive(g *Grant, vt, tt int64) bool {
	if g.RecordedAt > tt {
		return false // 审计时间尚不知悉该授权
	}
	return g.ValidFrom <= vt && vt < r.effectiveTo(g, tt)
}

// HoldsRole 判断用户在业务时间 vt、审计时间 tt 下是否实际持有角色。
func (r *Resolver) HoldsRole(user, role string, vt, tt int64) bool {
	return r.holdsRole(user, role, vt, tt, make(map[int64]bool))
}

func (r *Resolver) holdsRole(user, role string, vt, tt int64, visiting map[int64]bool) bool {
	for _, g := range r.grants {
		if g.Grantee != user || g.Role != role {
			continue
		}
		if !r.grantActive(g, vt, tt) {
			continue
		}
		if g.Grantor == "" {
			return true // 系统直接授权，链的根
		}
		if visiting[g.ID] {
			continue // 委托环保护
		}
		visiting[g.ID] = true
		// 委托仅在授予者本人同期实际持有该角色时有效
		if r.holdsRole(g.Grantor, role, vt, tt, visiting) {
			return true
		}
		delete(visiting, g.ID)
	}
	return false
}

// EffectivePermissions 返回用户在业务时间 vt、审计时间 tt 下的
// 实际有效权限集合（排序后输出，保证确定性）。
func (r *Resolver) EffectivePermissions(user string, vt, tt int64) []string {
	roles := make(map[string]bool)
	for _, g := range r.grants {
		if g.Grantee == user {
			roles[g.Role] = true
		}
	}
	perms := make(map[string]bool)
	for role := range roles {
		if !r.HoldsRole(user, role, vt, tt) {
			continue
		}
		for p := range r.rolePerm[role] {
			perms[p] = true
		}
	}
	out := make([]string, 0, len(perms))
	for p := range perms {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}
