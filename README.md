# GraphQL 字段级查询授权

`example.org/netsec/q0228` 提供可直接导入 Go GraphQL 网关的查询授权组件。它不依赖第三方 GraphQL 库，覆盖可执行文档的解析、计划归一化、运行时变量求值和逐别名授权。

## 安全语义

- 每个字段选择出现都生成独立授权节点，包括重复别名、片段展开、内联片段和条件分支。节点绑定精确响应路径（使用别名，而非字段名）。
- 同一响应路径只有在调用者身份、字段名、参数和完整授权结果（`Allowed`、`Scope`、`Grant`、`PolicyVersion`、`Subject`、`Fingerprint`、`MaxPageSize`）都相同时才允许合并。任一活动别名未授权，整条路径被拒绝。
- `@skip`/`@include` 在运行时按变量/默认值求值；随后每个活动出现仍单独调用策略。被跳过的分支不会被当作可达字段授权。
- 变量只解析为参数，不能改变、拼接或扩展选择集。参数按出现独立解析；同一响应键上的不同参数视为冲突，拒绝合并。分页大小可由 `Decision.MaxPageSize` 再收紧。
- 默认启用跨租户节点 ID 防护：从 `id`、`nodeId`、`ids`、`tenantId` 等参数提取显式租户前缀（含常见 base64 编码）并拒绝跨租户值。
- `Plan.Digest` 只依赖归一化后的操作形状，不含批处理元数据、持久化查询 ID、身份或运行时变量；批处理、持久化查询和直接执行同一操作共享摘要。
- 路径使用长度前缀键和安全字符串表示，`__proto__`、`constructor` 等别名只作为字面响应键，不会参与对象原型查找。

## 使用

```go
engine := netsec.NewEngine(netsec.PolicyFunc(
    func(ctx context.Context, req netsec.FieldRequest) (netsec.Decision, error) {
        if req.FieldName == "billing" && req.Identity.TenantID != "tenant-a" {
            return netsec.Deny("not permitted"), nil
        }
        decision := netsec.Allow("read")
        decision.MaxPageSize = 50
        return decision, nil
    },
))

plan, err := engine.Authorize(ctx, netsec.Request{
    Query:     query,
    Variables: variables,
    Identity:  netsec.Identity{TenantID: "tenant-a", SubjectID: "alice"},
})
if err != nil {
    // 任一活动别名拒绝或无法安全合并时 err 满足
    // errors.Is(err, netsec.ErrUnauthorized)。
    return err
}

for _, field := range plan.Fields {
    // field.Path 是执行器必须使用的响应路径。
}
```

批处理和持久化查询使用 `Engine.AuthorizeBatch`、`Engine.RegisterPersistedQuery`、`Engine.AuthorizePersisted`。相同操作的 `plan.Digest` 相同；运行时授权结果另见 `plan.AuthorizationDigest`。

## 验证

仓库测试覆盖重复别名仅有第二个被拒绝、原型污染式别名、片段复用、跨片段/内联片段条件、变量默认值扩大分页、跨租户节点 ID、批处理/持久化摘要一致性，以及合法的多别名查询。

运行：

```sh
go test ./...
go test -race ./...
```
