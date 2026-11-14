# 链式搜索患者范围组件

健康数据接口允许 `_include` 与链式搜索。攻击者可以从自己的资源出发引用其他患者的观察记录；如果服务端只校验起始资源，就会把后续引用当成可信数据返回。本模块提供一个可导入的 Go 组件，对基础搜索和每一次引用展开使用同一个授权谓词。

## 使用

```go
engine := netsec.NewEngine(store)
scope := netsec.NewScope("tenant-a", "patient-1")

result, err := engine.SearchQuery(
    ctx,
    scope,
    "Encounter",
    "subject:Patient.name=Alice&_include:iterate=Encounter:subject&_count=20&_offset=0",
)
if err != nil {
    return err
}

// result.Resources 是已授权分页，result.Included 是逐跳校验后的 include。
// result.Total 与分页使用完全相同的过滤结果。
```

`netsec.Store` 只需要实现两个按租户读取的方法：

```go
Get(ctx, tenantID, resourceType, resourceID) (*netsec.Resource, error)
List(ctx, tenantID, resourceType) ([]*netsec.Resource, error)
```

`netsec.MemoryStore` 可直接用于测试或嵌入式服务。`Resource` 支持独立的
`PatientID`、`Attributes`、`References` 与 `Shared` 字段；共享的
Practitioner/Organization 等资源可以显式标记为 `Shared`。

## 安全边界

- 基础搜索先按租户、患者集合、可选资源类型白名单过滤。
- 链式搜索的每一跳都重新读取目标资源，并在进入下一跳前重新授权；未授权中间节点会直接终止该路径。
- `_include` / `_revinclude` 只返回通过同一授权检查的目标，非法引用被静默丢弃，不会扩大 `Total` 或分页范围。
- `_include:iterate` / `_revinclude:iterate` 有深度上限和解析预算；过深或过宽的搜索会失败关闭（`ErrTooDeep` / `ErrBudgetExceeded`）。
- 不认识的 `_` 参数以及无法安全解释的 `_has`、`_filter`、`_query` 会返回 `ErrUnsupported`，避免因忽略过滤条件而扩大范围。
- 每次都从调用方传入的 `Scope` 创建快照，不跨请求缓存授权结果；范围改变后的下一次搜索立即生效。
- 结果在分页前按 `Type/ID` 稳定排序；`Total`、`HasMore` 和页内容由同一已授权集合计算。

## 验证

`search_test.go` 覆盖：

- 跨患者 `_include`、`_revinclude` 与冲突 owner 字段；
- 链式搜索在未授权中间跳终止；
- 分页 offset 遍历、`_count=0` 与 `Count()` 的过滤一致性；
- 合法患者、共享 Organization/Practitioner 引用与多跳合法链；
- 租户隔离、资源类型白名单、患者 link 不传递授权；
- 范围在 p1 / p2 / 组合 / 空集合之间切换时不残留旧结果；
- 深层链上限、格式错误的参数以及并发范围隔离。

运行：

```sh
go test ./...
go test ./... -race
```
