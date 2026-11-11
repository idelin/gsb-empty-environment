# 变异字典污染隔离

不同租户共享模糊测试平台，自动提取的令牌可能把一个租户秘密带进另一个租户样本。实现供Go变异宿主调用的字典包，词条绑定来源租户、输入摘要与可公开证明，合并只在授权交集内进行；撤销来源使派生词条失效。用多级派生、同字节不同来源和并发快照验证不能因内容相同而丢失限制。

## 包结构

包名 `netsec`（module `example.org/netsec/q0413`），仅依赖 Go 标准库：

- `doc.go`：安全模型与不变量说明。
- `digest.go`：`Digest`（SHA-256）与 `SumDigest`，把词条钉在具体输入上。
- `types.go`：`Tenant`、`Origin`、`Entry` 与溯源排序/防御性拷贝工具。
- `dictionary.go`：`Dictionary` 的添加、派生、撤销、授权判定与快照。
- `snapshot.go` / `import.go`：不可变快照与授权交集内的 `Merge`。
- `errors.go`：哨兵错误。

## 核心不变量

1. **溯源绑定**：每个 `Entry` 携带 `Origin{Tenant, Input, Public}` 闭包；词条按
   身份（单调 ID）而非字节内容区分，同字节不同来源是独立词条，限制与生命周期互不影响。
2. **派生只增不减**：`Derive` 取全部父词条溯源的并集，深度为最深父词条 +1；
   `Public` 证明永远不能清除另一来源的 secret 污点。
3. **授权交集**：`Snapshot(authorized)` 只暴露存活且全部 secret 租户都在授权集合
   内的词条；纯 public 词条可跨租户共享，但仍保留溯源以便审计与撤销。
4. **撤销级联**：`Revoke(tenant, input)` 使闭包含该来源的所有词条（含多级派生）
   立即失效，并阻止从该来源再次添加/派生/合并；`RevokeTenant` 按租户撤销其 secret 来源。
5. **并发与不可变**：`sync.RWMutex` 保护全部变更；快照为深拷贝，后续增删改与撤销
   不影响已取出的快照，`Merge` 基于快照因此天然限定在授权交集内。

## 典型用法

```go
dict := netsec.New()
id, err := dict.Add(token, netsec.Origin{
    Tenant: netsec.Tenant("tenant-a"),
    Input:  netsec.SumDigest(rawInput),
    Public: false, // 非公开材料
})
derived, err := dict.Derive(mutated, id)

snap, err := dict.Snapshot([]netsec.Tenant{"tenant-a"})
for _, entry := range snap.Entries() {
    _ = entry.Word // 仅包含授权交集内的存活词条
}

err = dict.Revoke("tenant-a", origin.Input) // 级联失效全部派生词条
```

## 测试

```sh
go test -race -count=1 ./...
```

覆盖：参数校验与跨租户拦截、同字节异源限制保留、多级派生污点累积与撤销级联、
public 不可降级 secret、`Merge` 不扩大授权、快照防御性拷贝与不可变性，以及
并发增删/撤销/快照的竞态检测。
