# impersonate

Kubernetes 风格的模拟（impersonation）授权包，纯标准库实现，可直接导入 Go API
服务器。包路径：

```go
import "prescreen.local/ps-ecb6db5562/impersonate"
```

## 安全语义

- 每个被模拟对象都是一次独立的 `impersonate` 授权检查，任一不通过则整个请求
  返回 `403`，下游 handler 不会执行：
  - 用户：资源 `users`，`resourceName` 为用户名；
  - 组：资源 `groups`，`resourceName` 为组名（逗号合并、去重后逐组检查）；
  - ServiceAccount：`system:serviceaccount:<ns>:<name>` 映射到核心组资源
    `serviceaccounts`，带 namespace，`resourceName` 为 SA 名；
  - UID：资源 `uids`；
  - Extra 键：资源 `userextras`、subresource 为解码并转小写后的键，即
    `userextras/<键>`。
  - `Rule.Name`（或 `Attributes.Name`）为空表示不限定 `resourceName`，否则必须
    精确匹配。
- `Impersonate-Group`、`Impersonate-Uid`、`Impersonate-Extra-*` 出现而没有
  `Impersonate-User` 时一律拒绝。
- `Impersonate-User`（及 `Impersonate-Uid`）只允许单值：重复头或逗号合并的多值
  都按冲突拒绝。
- Extra 头的键先 `url.PathUnescape` 百分号解码再转小写；非法百分号序列报错，
  解码后仍含 `%`（即二次编码伪造键）也拒绝，防止用 `%256164` 之类绕过已授权的键。
- 检查通过后，请求上下文中的身份被整体替换为被模拟身份：原身份的组、extra、
  权限均不叠加；审计事件（`AuditSink`）同时记录原身份与被模拟身份，允许和拒绝
  都会记录。

## 用法

```go
auth := &impersonate.RuleAuthorizer{Rules: []impersonate.Rule{
    {Resource: impersonate.ResourceUsers, Name: "alice"},
    {Resource: impersonate.ResourceGroups, Name: "platform"},
}}
sink := impersonate.AuditSinkFunc(func(ctx context.Context, e impersonate.AuditEvent) {
    log.Printf("impersonation allowed=%t %s -> %s", e.Allowed,
        e.Requester.GetUserName(), e.Impersonate.GetUserName())
})

// 认证层先用 impersonate.WithUser 放入请求者，再套模拟中间件。
handler := impersonate.Middleware(auth, sink)(myAPIHandler)
```

下游通过 `impersonate.UserFrom(ctx)` 拿到的永远是被模拟身份（无模拟头时为原
身份）。完整可运行示例见 `cmd/impersonation-example`。

## 测试

`go test ./...` 覆盖题目要求的场景：只授权用户却附加组、多值 User 头冲突、
extra 键二次编码绕过、无 User 的 Group/UID/Extra、权限不叠加，以及 SA/UID/
resourceName 等资源映射。
