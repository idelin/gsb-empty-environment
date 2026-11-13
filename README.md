# 角色创建与绑定的提权预防检查

能创建角色或绑定的低权限用户，可能给自己写出更大的权限。为Go准入组件写可导入的 Kubernetes 授权检查包：创建或更新 Role 时其规则须被请求者现有权限覆盖，否则需要 roles 的 escalate 动词；创建绑定时被引用角色的权限须被覆盖，否则需要该角色的 bind 动词；覆盖按 apiGroups、resources（含 pods/exec 与 *）、verbs、resourceNames 判定，命名空间内绑定只看该命名空间权限。用多一个动词、子资源、resourceNames 缩窄和 bind 豁免验证。

## 使用

```go
import (
	"context"

	netsec "example.org/netsec/q0832"
)

// authorize 通常封装 Kubernetes authorizer 或 SubjectAccessReview 客户端。
authz := netsec.AuthorizeFunc(func(ctx context.Context, attrs netsec.Attributes) bool {
	// attrs.Namespace、APIGroup、Resource、Verb、Name 分别对应授权属性。
	return subjectCan(ctx, attrs)
})

err := netsec.CheckRole(ctx, authz, netsec.Role{
	Name:      "debug-reader",
	Namespace: "team-a",
	Rules: []netsec.PolicyRule{{
		APIGroups: []string{""},
		Resources: []string{"pods", "pods/log"},
		Verbs:     []string{"get", "list"},
	}},
})
if err != nil {
	// errors.Is(err, netsec.ErrEscalation) 表示缺少覆盖权限且无 escalate。
}
```

绑定检查需要调用方先从可信数据源读取被引用对象的规则，避免使用请求中不可信的内容：

```go
err := netsec.CheckRoleBinding(ctx, authz, netsec.RoleBinding{
	Name:      "debug-reader-binding",
	Namespace: "team-a",
	RoleRef: netsec.RoleRef{
		APIGroup: netsec.RBACAPIGroup,
		Kind:     netsec.KindRole,
		Name:     "debug-reader",
	},
}, referencedRole.Rules)
if err != nil {
	// errors.Is(err, netsec.ErrBinding) 表示缺少覆盖权限且无 bind。
}
```

## 判定规则

- 授权覆盖是单向的：请求者规则中的 `*` 可覆盖具体值，具体值不能覆盖请求规则中的 `*`。
- `pods` 不覆盖 `pods/exec`；必须显式授予 `pods/exec`，或使用资源 `*`。
- 请求规则没有 `resourceNames` 时表示所有名称，只有同样未按名称收窄的授权才能覆盖；请求规则有名称为授权名称的子集时可以覆盖。
- 多条现有规则可共同覆盖一条新规则的全部权限组合。
- `escalate` 和 `bind` 查询也会带上 Role 名称；若授权被 `resourceNames` 收窄，只豁免对应角色。
- `RoleBinding` 引用 `ClusterRole` 时，其规则仍在绑定的命名空间内检查；空命名空间表示集群范围。
