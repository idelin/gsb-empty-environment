# 消息主题的字符串前缀放大了订阅权限

多租户消息平台用字符串前缀授权，攻击者订阅`tenant-a`却因`tenant-a-`规则读到另一租户的`tenant-a-secret`主题。请实现供Go消息代理调用的可导入资源模式授权组件。主题、消费组和事务ID各自建模为字面名称或显式前缀/通配模式，模式必须与资源类型匹配，前缀边界按分隔符判定而不是字符串包含；拒绝优先级高于允许，跨租户组名不得借重命名复用。ACL变更按代次提交，进行中的订阅在代次更新后重新核验，缓存不得延长旧结论。用相似前缀、大小写、空名称、模式类型错配、并发ACL更新和合法同租户订阅验证不越过租户边界。

## 组件设计 (`netsec`)

纯标准库实现，无外部依赖，可直接被消息代理 `import` 并嵌入。

### 资源与模式

- `ResourceKind`：`KindTopic` / `KindGroup` / `KindTransaction`，三类资源严格隔离。
- `Pattern` 绑定一个资源类型，`PatternType` 取三选一：
  - `TypeLiteral`：只匹配完全相等、大小写敏感的名称，长字符串永远不会被短字面量命中。
  - `TypePrefix`：仅在“完全相等”或“下一个字符是分隔符”时匹配，绝不做字符串 `contains`。
    因此 `tenant-a` 不会命中 `tenant-ab`；带尾分隔符的 `tenant-a-` 表示纯命名空间前缀，
    不匹配裸名 `tenant-a`，只匹配 `tenant-a-...` 子名称。
  - `TypeWildcard`：按分隔符切段逐段 glob，`*` 不跨分隔符，段数必须一致。
- 模式与资源的 `Kind` 不一致时该规则完全不参与判定（`ErrKindMismatch` 语义由默认拒绝兜底），
  主题规则不可能授权同名消费组或事务 ID。
- 空名称、首尾/重复分隔符、未知类型一律校验失败。

### 判定与代次

- 拒绝优先：同一主体、资源、动作上只要存在命中的 deny，即拒绝；否则需有命中的 allow 才放行，默认拒绝。
- 动作是位掩码（`ActionRead` / `ActionWrite` / `ActionAdmin`），条目必须覆盖请求的全部位。
- `ReplaceACL` 先整体校验再原子提交，代次 `Generation()` 单调递增；任一条目非法则旧 ACL 原样保留。
- 决策缓存按代次键控，提交新代次（及组属主变更）时整体丢弃，缓存不可能延长旧结论。
- `OpenSubscription` 返回带代次的 `Subscription`；ACL 更新后调用 `Reverify` 按最新快照重新核验，
  失效时返回包装 `ErrSubscriptionStale` 与 `ErrNotAuthorized` 的错误，代理据此下线订阅。

### 消费组属主

- `BindGroup` 把组名绑定到属主；其他租户绑定同名组返回 `ErrGroupOwnerConflict`。
- `RenameGroup` 要求属主一致、新旧名称均通过当前代次的 `ActionAdmin` 校验，
  且目标名未被其他租户占用，堵住跨租户改名复用。

### 快速上手

```go
auth := netsec.NewAuthorizer('-')
gen, err := auth.ReplaceACL([]netsec.Entry{
    {Principal: "tenant-a", Effect: netsec.EffectAllow, Actions: netsec.ActionRead,
        Pattern: netsec.Pattern{Kind: netsec.KindTopic, Type: netsec.TypeLiteral, Value: "tenant-a"}},
    {Principal: "tenant-a", Effect: netsec.EffectDeny, Actions: netsec.ActionRead,
        Pattern: netsec.Pattern{Kind: netsec.KindTopic, Type: netsec.TypeLiteral, Value: "tenant-a-secret"}},
})

if err := auth.AuthorizeTopic("tenant-a", "tenant-a", netsec.ActionRead); err != nil {
    // 处理拒绝
}
```

### 测试

`go test ./...`（另可用 `-race`）覆盖：相似前缀、大小写敏感、空/畸形名称、模式类型错配、
拒绝优先、代次提交与缓存失效、并发 ACL 更新、跨租户组名复用、同租户合法订阅与租户边界。
