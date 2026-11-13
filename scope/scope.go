// Package scope 实现镜像仓库令牌服务的 scope 授权求交。
//
// 一个 scope 形如 "repository:命名空间/名称:动作1,动作2"，一次请求可以携带
// 多个 scope 参数。本包先对 scope 做严格校验与合并，再与调用方给出的用户
// 权限表求交，最终得到令牌中应携带的授权集合。
package scope

import "strings"

// 支持的仓库动作。
const (
	ActionPull   = "pull"
	ActionPush   = "push"
	ActionDelete = "delete"
)

// 支持的资源类型。
const (
	TypeRepository = "repository"
	TypeRegistry   = "registry"
)

// CatalogScope 是唯一被接受的 catalog scope 字面量。
const CatalogScope = "registry:catalog:*"

// Scope 是一个通过校验的 scope 请求。
type Scope struct {
	Type       string   // 资源类型，repository 或 registry
	Repository string   // 仓库名；catalog 资源为空
	Actions    []string // 请求的动作（已去重，规范顺序）；catalog 为 ["*"]
}

// Permission 是用户权限表中的一条权限。
//
// RepositoryPattern 支持三种形态：
//   - "*"：匹配任意仓库；
//   - 以 "/*" 结尾的分段前缀，如 "team-a/*" 匹配 "team-a/..." 下的仓库；
//   - 精确仓库名，如 "team-a/app"。
//
// Registry:catalog:* 是表示 catalog 列表权限的特殊条目，Actions 中含
// "list" 即视为拥有列表权限。
type Permission struct {
	RepositoryPattern string
	Actions           []string
}

// Grant 是令牌中为某个资源实际授予的结果。
type Grant struct {
	Type       string
	Repository string
	Actions    []string
	// Wildcard 为 true 时表示 catalog 授权，String 渲染为 registry:catalog:*。
	Wildcard bool
}

// String 按令牌服务的传输格式渲染授权结果。
// 动作集为空时仍渲染出该资源（catalog 渲染为 registry:catalog:）。
func (g Grant) String() string {
	if g.Type == TypeRegistry {
		if g.Wildcard {
			return CatalogScope
		}
		return "registry:catalog:"
	}
	return TypeRepository + ":" + g.Repository + ":" + strings.Join(g.Actions, ",")
}

// Parse 解析并校验单个原始 scope 字符串，非法时返回 ok=false（整条丢弃）。
func Parse(raw string) (Scope, bool) {
	if raw == CatalogScope {
		return Scope{Type: TypeRegistry, Actions: []string{"*"}}, true
	}

	parts := strings.Split(raw, ":")
	if len(parts) != 3 || parts[0] != TypeRepository {
		return Scope{}, false
	}

	repository := parts[1]
	if !validRepository(repository) {
		return Scope{}, false
	}

	if parts[2] == "" {
		return Scope{}, false
	}
	seen := map[string]bool{}
	var actions []string
	for _, action := range strings.Split(parts[2], ",") {
		if !isKnownAction(action) {
			return Scope{}, false
		}
		if !seen[action] {
			seen[action] = true
			actions = append(actions, action)
		}
	}

	return Scope{
		Type:       TypeRepository,
		Repository: repository,
		Actions:    sortActions(actions),
	}, true
}

// Authorize 对请求中的多个原始 scope 与用户权限表求交，返回令牌授权结果。
//
// 非法 scope 被整条丢弃；同一仓库的多个合法 scope 先取动作并集，再与权限
// 求交；求交为空的 scope 仍保留在结果中并带空动作集。结果按各合法 scope
// 首次出现的顺序排列。
func Authorize(rawScopes []string, permissions []Permission) []Grant {
	type entry struct {
		scope Scope
	}

	var order []string
	merged := map[string]*entry{}

	for _, raw := range rawScopes {
		parsed, ok := Parse(raw)
		if !ok {
			continue
		}

		key := parsed.Type + "\x00" + parsed.Repository
		if existing, found := merged[key]; found {
			existing.scope.Actions = actionsToSortedSlice(union(existing.scope.Actions, parsed.Actions))
			continue
		}

		order = append(order, key)
		merged[key] = &entry{scope: parsed}
	}

	grants := make([]Grant, 0, len(order))
	for _, key := range order {
		parsed := merged[key].scope

		if parsed.Type == TypeRegistry {
			grants = append(grants, authorizeCatalog(permissions))
			continue
		}

		allowed := allowedActions(parsed.Repository, permissions)
		granted := intersect(parsed.Actions, allowed)

		// push/delete 是写动作，只有同时拥有 pull 时才能授予。
		if contains(granted, ActionPush) || contains(granted, ActionDelete) {
			if !allowed[ActionPull] {
				delete(granted, ActionPush)
				delete(granted, ActionDelete)
			}
		}

		grants = append(grants, Grant{
			Type:       TypeRepository,
			Repository: parsed.Repository,
			Actions:    actionsToSortedSlice(granted),
		})
	}

	return grants
}

// authorizeCatalog 判断用户是否拥有 catalog 列表权限。
func authorizeCatalog(permissions []Permission) Grant {
	for _, permission := range permissions {
		if permission.RepositoryPattern != CatalogScope {
			continue
		}
		for _, action := range permission.Actions {
			if action == "list" {
				return Grant{Type: TypeRegistry, Actions: []string{"*"}, Wildcard: true}
			}
		}
	}
	return Grant{Type: TypeRegistry, Actions: []string{}}
}

// allowedActions 汇总权限表中对某仓库生效的全部动作。
func allowedActions(repository string, permissions []Permission) map[string]bool {
	allowed := map[string]bool{}
	for _, permission := range permissions {
		if !patternMatches(permission.RepositoryPattern, repository) {
			continue
		}
		for _, action := range permission.Actions {
			if isKnownAction(action) {
				allowed[action] = true
			}
		}
	}
	return allowed
}

// patternMatches 判断权限模式是否匹配给定仓库（按路径分段匹配）。
func patternMatches(pattern, repository string) bool {
	switch {
	case pattern == "*":
		return true
	case strings.HasSuffix(pattern, "/*"):
		prefix := strings.TrimSuffix(pattern, "/*")
		return strings.HasPrefix(repository, prefix+"/")
	default:
		return pattern == repository
	}
}

// validRepository 按小写字母数字、. _ - 与 / 分段校验仓库名。
// 含空段、".." 段或大写字母时拒绝。
func validRepository(repository string) bool {
	if repository == "" {
		return false
	}
	for _, segment := range strings.Split(repository, "/") {
		if segment == "" || segment == ".." {
			return false
		}
		for _, r := range segment {
			switch {
			case r >= 'a' && r <= 'z':
			case r >= '0' && r <= '9':
			case r == '.' || r == '_' || r == '-':
			default:
				return false
			}
		}
	}
	return true
}

func isKnownAction(action string) bool {
	return action == ActionPull || action == ActionPush || action == ActionDelete
}

func intersect(requested []string, allowed map[string]bool) map[string]bool {
	result := map[string]bool{}
	for _, action := range requested {
		if allowed[action] {
			result[action] = true
		}
	}
	return result
}

func union(a, b []string) map[string]bool {
	result := map[string]bool{}
	for _, action := range a {
		result[action] = true
	}
	for _, action := range b {
		result[action] = true
	}
	return result
}

func contains(actions map[string]bool, action string) bool {
	return actions[action]
}

func actionsToSortedSlice(actions map[string]bool) []string {
	if len(actions) == 0 {
		return []string{}
	}
	var result []string
	for _, action := range []string{ActionPull, ActionPush, ActionDelete} {
		if actions[action] {
			result = append(result, action)
		}
	}
	return result
}

func sortActions(actions []string) []string {
	set := map[string]bool{}
	for _, action := range actions {
		set[action] = true
	}
	return actionsToSortedSlice(set)
}
