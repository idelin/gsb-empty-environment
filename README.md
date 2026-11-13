# SECURITY DEFINER 解析包

该包把高权限函数中的 `table`、`function`、`operator` 引用冻结为
`(ObjectID, Version)`。执行阶段不再按名称解析；回调只接收冻结对象 ID，从
根源上阻断 `search_path` 遮蔽、临时表注入和同名重建攻击。

## 核心模型

- `Catalog` 保存 `Schema`（稳定 `ID`、可变 `Name`、`Owner`、只读/系统/
  临时/PUBLIC 可写标记）与 `Object`（`ID`、`Kind`、`Owner`、`Version`）。
- `FunctionDefinition` 包含 `DefinerID`、固定 `SearchPath`、引用 `AST` 和
  `AllowedCallers`。
- `Catalog.Publish` 校验路径并冻结所有安全关键引用，返回
  `PublishedFunction` 和可审计的 `DependencyManifest`。
- `Engine.Invoke` 先以调用者身份求值参数，再以定义者身份执行冻结对象。

## 发布约束

发布时拒绝：

- `SearchPath` 中使用 schema 名称而非目录中存在的 `SchemaID`；
- 首项不是只读系统 schema；
- `$user`、临时 schema、PUBLIC 可写 schema；
- 后续 schema 可由 definer 之外的任何主体写入；
- 解析到临时 schema、冻结路径之外的限定引用、无匹配或重载歧义；
- 已标记撤销的函数定义。

未限定引用按冻结路径顺序解析，第一个匹配项生效。函数和 operator 使用
`Signature`/`ArgTypes` 区分重载。

## 执行约束

每次 `Invoke` 都会重验：

- 所有冻结对象的 ID、版本、kind、名称和签名；
- 冻结路径及 schema 当前写权限；
- 定义者函数未被 `Revoke`，其函数对象未 DROP；
- 嵌套定义者函数同样满足以上条件。

`DynamicSQL` 默认返回 `ErrDynamicSQLDenied`。只有
`Invocation.TrustedDynamicAST` 提供对应 key 的 AST 时，才会在冻结路径下
重新解析每个引用并执行。

## 依赖清单与劫持证据

`PublishedFunction.Manifest()` 返回每个引用的候选 schema、命中的
`ObjectID`/`Version` 和搜索位置。`ShortestHijackEvidence` 使用 BFS 沿嵌套
定义者调用寻找最短证明；候选只包括冻结对象之前的、攻击者当前可写的
schema，并给出攻击者需创建的同名/同签名对象信息。对于本应被安全发布拒绝
的目录，`FindShortestHijack` 可单独做威胁建模，报告已落入攻击者可写
schema 的解析位置。

## 示例

```go
c := netsec.NewCatalog()
_ = c.AddSchema(netsec.Schema{
    ID: "sys", Name: "pg_catalog", Owner: "system",
    System: true, ReadOnly: true,
})
_ = c.AddSchema(netsec.Schema{ID: "app", Name: "app", Owner: "definer"})
_ = c.AddObject(netsec.Object{
    ID: "accounts_v1", SchemaID: "app", Kind: netsec.KindTable,
    Name: "accounts", Version: 1,
})

pub, err := c.Publish(netsec.FunctionDefinition{
    ID: "secure_read", DefinerID: "definer",
    SearchPath: []netsec.SchemaID{"sys", "app"},
    AST: netsec.AST{netsec.TableRef("accounts")},
    AllowedCallers: map[netsec.PrincipalID]bool{"alice": true},
})

engine := netsec.NewEngine(c, func(call netsec.FrozenCall) (any, error) {
    return call.ObjectID, nil // 宿主按 ObjectID 执行，不再次解析名称
})
_ = engine.Register(pub)
_, err = engine.Invoke(pub, netsec.Invocation{CallerID: "alice"})
```

`go test ./...` 覆盖函数遮蔽、operator 重载、临时 schema、参数副作用、
动态 SQL、嵌套定义者函数、schema 权限回退、版本变化及 DROP 后同名重建。
