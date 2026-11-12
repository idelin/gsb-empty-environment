# OAuth资源指示符受众绑定

本包实现 RFC 8707 resource 参数的安全处理：授权请求批准的资源集合是固定上限，token 请求只能缩小，不能扩大。包内不负责签名或 HTTP 路由，只产生必须写入令牌的 `aud` 与 `scope`。

## 使用方式

```go
approval, err := netsec.NewApproval([]netsec.ResourceGrant{
	netsec.NewResourceGrant("https://api.example.com/", "read", "write"),
	netsec.NewResourceGrant("https://files.example.com/", "read", "write"),
})
if err != nil {
	// resource 非法或批准集合冲突
}

// token 请求可以指定一个或多个已批准 resource；都必须是批准集合的子集。
token, err := approval.IssueToken(netsec.TokenRequest{
	Resources: []string{"https://files.example.com/"},
})
if netsec.IsInvalidTarget(err) {
	// 越权 resource、带 fragment 的 URI、或多个批准 resource 但未指定
}

// token.Audience 精确等于本次请求的 resource 集合；
// token.Scope 是这些 resource 批准 scope 的交集。
```

单 resource 令牌的 `aud` 序列化为字符串，多 resource 令牌为数组。只有批准集合恰好包含一个 resource 时，省略 token 请求的 resource 才会自动使用该 resource；批准多个而未指定时返回 `invalid_target`。

## 刷新与资源服务器

```go
refresh := netsec.NewRefreshGrant(approval) // 保留原始授权批准集合
next, err := refresh.Issue(netsec.TokenRequest{
	Resources: []string{"https://api.example.com/"},
})
```

刷新路径仍使用原始批准集合，新增 resource 返回 `invalid_target`，scope 也不能增加。资源服务器应使用 `ResourceServerAccepts("https://api.example.com/", aud)` 或 `aud.Contains`，内部是可重复值的逐字节相等比较，不做前缀、主机后缀或大小写匹配。

## 主要 API

- `ValidateResource` / `ParseResources`：校验绝对 URI、拒绝 fragment、去重并保留原始字符串。
- `NewApproval` / `NewApprovalFromScopes`：快照授权请求批准的资源与各资源 scope。
- `Approval.ResolveTokenResources`：执行子集规则和唯一 resource 缺省规则。
- `Approval.IssueToken` / `IssueToken`：生成精确 `aud` 和 scope 交集。
- `NewRefreshGrant` / `RefreshToken`：在原批准集合内签发刷新后的令牌。
- `Audience` / `ResourceServerAccepts`：严格 audience 判定和 JSON `aud` 编解码。

OAuth 授权服务器签发令牌时，攻击者在 token 请求里改 resource 参数，想让一个令牌被多个资源服务器接受。为Go授权服务器写可导入的 RFC 8707 resource 参数处理包：resource 须是不含 fragment 的绝对 URI，可重复；授权请求里批准的 resource 集合固定，token 请求的 resource 只能是其子集，缺省时取批准集合中唯一的一个，多于一个而未指定则返回 invalid_target；签出令牌的 aud 恰为所请求的 resource（多个时为数组），scope 按调用方给出的各 resource 批准范围取交集；刷新令牌换新令牌时同样受限于原批准集合且不能新增；资源服务器按自身标识逐字节比较 aud，不做前缀匹配。测超集、fragment、未指定多个、刷新扩增。
