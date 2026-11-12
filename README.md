# Impersonate头授权求值

Kubernetes 风格的 API 服务器允许持有 impersonate 权限的主体用 Impersonate-User 与 Impersonate-Group 头代为请求，攻击者借此提升身份，例如只拿到模拟某个用户的权限，却附加 system:masters。为Go的 API 服务器写可导入的模拟授权包：被模拟的每个对象（用户、组、serviceaccount、uid、extra 键）都是一次独立授权检查，动词为 impersonate，资源为 users、groups、serviceaccounts、uids、userextras/<键>，resourceNames 可限定；Impersonate-Group 出现而没有 Impersonate-User 拒绝；Impersonate-Extra-<键> 的键须百分号解码并转小写后再检查；任何一项不被允许则整个请求 403；通过后审计同时记录原身份与被模拟身份，之后的授权只使用被模拟身份，原身份的权限不叠加。测只授权用户却附加组、extra 键编码绕过、无 User 的 Group、权限叠加。

## 包结构（`package netsec`，纯标准库）

- `impersonate.go`：`Check` 核心求值，每个被模拟对象一次独立 `impersonate` 授权检查。
- `middleware.go`：`Middleware` HTTP 包装器、上下文身份传递、审计事件与 403 JSON 响应。
- `rule_authorizer.go`：RBAC 风格的 `RuleAuthorizer`，无匹配规则即不放行。

## 授权模型

- 动词固定为 `impersonate`；资源分别为 `users`、`groups`、`serviceaccounts`、`uids`、`userextras/<键>`，`resourceNames` 可限定具体对象。
- 模拟 `system:serviceaccount:<ns>:<name>` 时，除 `users` 检查外，还要通过一次 `serviceaccounts` 检查。
- `Impersonate-Group` 缺少 `Impersonate-User` 直接拒绝（`ErrGroupWithoutUser`）。
- `Impersonate-Extra-<键>` 的键先 `url.QueryUnescape` 百分号解码、再转小写后检查，`R%6Fle` 与 `Role` 等价，无法用编码绕过。
- 任一检查失败：整个请求返回 `403 Forbidden` 并记录拒绝审计；全部通过后剥离模拟头，下游只看到被模拟身份。

## 用法

```go
authz := netsec.NewRuleAuthorizer(
    netsec.Rule{Subject: "alice", Verb: netsec.ImpersonateVerb,
        Resource: netsec.UserResource, ResourceNames: []string{"bob"}},
)
sink := netsec.AuditSinkFunc(func(_ context.Context, e netsec.AuditEvent) {
    log.Printf("impersonation allowed=%v requestor=%s impersonated=%+v",
        e.Allowed, e.Requestor.Name, e.Impersonated)
})

handler := netsec.Middleware{Authorizer: authz, Sink: sink}.Wrap(apiHandler)
http.ListenAndServe(":8443", handler)
```

认证中间件用 `netsec.WithUser` 放入原身份；下游处理器用 `netsec.UserFrom` 取到的是替换后的被模拟身份。

## 测试

```sh
go test -race ./...
```

覆盖：只授权用户却附加 `system:masters` 组、extra 键百分号编码绕过、无 User 的 Group、原身份与被模拟身份权限不叠加，以及 serviceaccount/uid 的 `resourceNames` 限定与 403 审计。
