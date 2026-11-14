# 批量导入合并策略（netsec）

供 Go 导入服务调用的可导入合并策略组件，防止批量导入按业务键写入时被用来覆盖他人数据或越权提升记录状态。

## 安全策略

- **主体 + 租户 + 业务键寻址**：`Merger.Import` 以 `Principal` 的租户为边界，行内租户与主体租户不一致直接拒绝（`ErrCrossTenant`）；同一业务键已存在于其他租户时，禁止在本租户建同名记录进行劫持。
- **覆盖需显式权限**：命中已有业务键时必须持有 `PermOverwrite`，否则拒绝（`ErrPermissionDenied`），原记录与版本历史均不变。
- **状态变更需发布权限**：覆盖时改变状态（如 `draft` → `published`），或新建记录直接落在非草稿状态，都需要 `PermPublish`；新建需要 `PermCreate`。
- **旧版本保留**：每次覆盖把旧版本归档到 `Store.Versions`，版本号单调递增，写入人留痕（`UpdatedBy`）。
- **重复键拒绝**：同一批次内业务键重复时，涉及的所有行均拒绝（`ErrDuplicateKey`），不产生任何写入。
- **导入后回读差异**：写入后逐行 `Get` 回读并按字段比对（`ReadBackReport.Diffs`），同时校验归档版本；并对导入前后全量快照做差，确认未被授权的记录逐字节未变。任何漂移都会令 `ImportReport.ReadBackVerified` 为 `false`。

## 用法

```go
store := netsec.NewMemoryStore()
merger := netsec.NewMerger(store)

report := merger.Import(netsec.ImportRequest{
    Principal: netsec.Principal{
        ID:       "user-1",
        TenantID: "tenant-a",
        Permissions: map[netsec.Permission]bool{
            netsec.PermCreate:    true,
            netsec.PermOverwrite: true,
        },
    },
    Records: []netsec.Record{
        {TenantID: "tenant-a", BusinessKey: "invoice-1", OwnerID: "user-1",
            Status: netsec.StatusDraft, Fields: map[string]string{"amount": "100"}},
    },
})

if !report.OK() {
    // 处理 report.Results 中各行的 Violation（可用 errors.Is 匹配哨兵错误）
}
```

生产环境可实现 `Store` 接口对接真实数据库；接口约定覆盖写入必须归档旧版本并递增版本号。

## 测试

`go test ./...` 覆盖：同名覆盖（无权限拒绝 / 有权限归档旧版本）、状态提升越权、跨租户与跨租户同名键、批内重复键、合法导入回读，以及存储层篡改与连带改动检测。
