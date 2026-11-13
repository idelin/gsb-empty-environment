# netsec: 安全编译计划缓存

`example.org/netsec/q0117` 提供一个可导入、fail-closed 的 Go 数据库计划缓存模型。计划身份不只绑定 SQL 文本，而是覆盖全部授权输入。

## 计划身份

`CompileInput`/`PlanKey` 覆盖：

- 规范查询 AST 摘要与参数类型列表摘要
- 调用主体 ID 与角色闭包摘要
- 每个引用对象的稳定 `ObjectID`、`SchemaVersion`、所需操作与 RLS 摘要
- 全局 `PolicyEpoch`

参数值不进入计划身份，执行时通过 `BindParameters` 进行结构化绑定。`Plan` 对外只返回依赖清单、输入和算子的副本；算子字段不可导出。

## 复核与事务

`Session.Execute` 在命中后、执行前重新核对主体是否活跃、角色闭包、`PolicyEpoch`、对象 ID/版本、RLS 摘要和权限。任一变化都会丢弃旧项并重新编译；复核失败时不会执行任何算子。

- 事务固定开始时的 `SecuritySnapshot`。
- `REVOKE ... IMMEDIATE` 会 fence 已开始事务和等待执行的计划。
- 普通 `REVOKE` 只影响下一事务。
- `SwitchPrincipal` 清空连接局部计划句柄；进程全局缓存保留，但新主体只能以自己的完整键命中。

## 用法

```go
snap := netsec.NewSnapshot(1)
snap.Principals["alice"] = netsec.PrincipalState{
    Active: true,
    Grants: map[netsec.ObjectID]netsec.OperationSet{"users": netsec.OpsSelect},
}
snap.Objects["users"] = netsec.ObjectState{
    ObjectID: "users", Name: "users", SchemaVersion: 3, RLSDigest: "rls:v4",
}

host := netsec.NewHost(snap)
session, _ := host.NewSession("alice")

params, _ := netsec.Bind([]netsec.ParamType{netsec.ParamInt}, 42)
result, err := session.Execute(netsec.ExecutionRequest{
    Query: netsec.Query{
        ASTDigest:  "ast:select-users",
        ParamTypes: []netsec.ParamType{netsec.ParamInt},
        Objects:    []netsec.ObjectRef{{ObjectID: "users", Operations: netsec.OpsSelect}},
    },
    Params: params,
})
```

`Host.Observe` 提供 `EventOperatorStart`/`EventOperatorEnd` 等可观察回调；`Host.SetExecutor` 接收结构化绑定参数。两者都可用于验证被拒绝的计划执行算子数为零。

## 弱 cacheKey 审计

`FindWeakKeyCounterexample` 在主体、角色闭包、对象版本、RLS 摘要和 epoch 各两个候选取值上做有限差分搜索，按 Hamming 距离返回“弱键相同但正确授权结果不同”的最短例。

```go
example, found := netsec.FindWeakKeyCounterexample(keyFn, authorizeFn, domain)
```

## 验证

```sh
go test -race ./...
go vet ./...
```
