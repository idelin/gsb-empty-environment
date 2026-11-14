# 别名安全的字段级授权组件

该组件用于 Go 搜索网关，修复“查询别名指向的底层索引时，字段策略未随索引
解析扩散”的越权问题。别名表达式会先绑定到一个不可变目录代次（generation），
解析成具体索引后，再把每个索引策略与可选别名策略取交集。字段只有被所有目标
索引同时允许时才会进入查询计划和响应。

```go
catalog := netsec.NewCatalog()
catalog.AddIndex(netsec.Index{
    Name:   "logs-a",
    Fields: []string{"public.id", "public.message", "private.secret"},
    Policy: netsec.Policy{AllowedFields: []string{"public.*"}},
})
catalog.AddIndex(netsec.Index{
    Name:   "logs-b",
    Fields: []string{"public.id", "public.message", "private.secret"},
    Policy: netsec.Policy{AllowedFields: []string{"public.id"}},
})
catalog.CreateAlias("logs", "logs-a", "logs-b")

authorizer := netsec.NewAuthorizer(catalog)
plan, safeRequest, err := authorizer.Authorize("logs", request)
// 只授予网关对 safeRequest 发起检索。
response := authorizer.FilterResponse(plan, backendResponse)
```

## 安全模型

- `Policy.AllowedFields` 是逐字段白名单。父字段 `profile` 不会隐式授予
  `profile.ssn`；需要显式写 `profile.*` 或 `profile.**`。零值策略默认拒绝全部
  字段，`AllowAll` 仅用于管理策略。
- `Policy.DeniedFields` 优先于允许列表，拒绝父字段会同时拒绝其后代。
- 请求中的 `_source`、`fields`、`docvalue_fields`、`stored_fields`、聚合、
  `script_fields`、`runtime_mappings`、`sort` 和高亮都会与有效集合求交。
  请求通配符只从已授权字段中做枚举，绝不会从索引 mapping 新增授权。
- 脚本中的 `doc[...]`、`params._source...`、`field(...)` 会静态提取字段引用。
  动态字段访问、读取整份 `_source`、或引用未授权字段的脚本会被移除；严格模式
  返回 `ErrUnauthorized`。读取父对象时，只有整个子树都被授权才允许，避免
  `params._source.profile` 绕过 `profile.ssn` 的限制。
- 运行时字段必须先由策略允许，其脚本还必须只读取已授权字段；它不会把新字段
  加入有效集合。
- 响应过滤不信任后端返回：`_source`、`fields`、高亮、`inner_hits`、聚合及
  聚合桶都会按同一计划再次裁剪。

## 代次与计划缓存

`Catalog` 的每次索引、策略、别名、别名写索引或重指向变更都会推进全局代次；
别名也记录自己的代次。`Plan.Generation`、`Plan.PolicyRevision` 与
`WriteTarget.Generation` 可用于审计和一致性检查。计划缓存以代次为作用域，
代次变化时整体作废，因此权限变化后不会复用旧允许结论。

`Plan.ValidAgainst(catalog)` 可检查计划是否仍对应当前目录。在途请求可以继续用
其旧代次计划过滤响应；新请求必须重新调用 `Authorize`。若网关要求响应阶段也
必须处于当前代次，可使用 `FilterResponseCurrent`，过期计划返回 `ErrStalePlan`。

## 模式

- `NewAuthorizer`：删除越权字段并记录到 `plan.Violations`。
- `NewAuthorizerWithConfig(..., Config{Strict: true})`：仍返回已清洗的请求，
  同时返回 `*AuthorizationError`（可用 `errors.Is(err, netsec.ErrUnauthorized)`
  判断）。
- `ResolveWrite` / `AuthorizeDocument`：按别名写索引及代次处理写入侧。

## 验证

```sh
go test ./...
go test -race ./...
```

测试覆盖别名双指向策略交集、通配字段、脚本读取、运行时字段、聚合桶、索引
重指向与策略变更、写别名代次、父字段不扩权，以及合法字段保留。
