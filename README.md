# groupx

多租户分组计算库（Go，无第三方依赖，兼容 Go 1.11+）。

## 隔离保证

- **去重按租户隔离**：同一 ID 在不同租户互不去重，仅在所属租户内去重。
- **缓存按租户隔离**：每个租户持有私有 LRU 缓存，淘汰互不影响，API 无法跨租户读取。
- **检查点按租户隔离**：`Checkpoint`/`Restore` 只快照、回滚单个租户的去重集、聚合、缓存、水位与偏移。
- **水位按租户隔离**：每个租户独立推进 event-time 水位与最高偏移。
- **跨租户合并仅经显式授权**：`IssueMergeToken`（需 admin key）签发一次性令牌，
  `Merge` 校验令牌与源集合/目标完全匹配后，把结果写入**全新命名空间**；源租户不被修改，
  令牌一次性，目标不得已存在、不得是源租户。未配置 admin key 时合并能力整体关闭。

## 快速示例

```go
e := groupx.New(1024, "admin-secret")
e.Ingest(groupx.Record{Tenant: "A", Group: "g", ID: "k1", Payload: "pa", Value: 1, Ts: 10, Seq: 1})
e.Ingest(groupx.Record{Tenant: "B", Group: "g", ID: "k1", Payload: "pb", Value: 100, Ts: 20, Seq: 2})
// 同 ID 不同租户：两条都生效，互不去重

e.Checkpoint("A")
e.Restore("A") // 仅回滚 A，B 不受影响

tok, _ := e.IssueMergeToken("admin-secret", []string{"A", "B"}, "M")
e.Merge(tok, []string{"A", "B"}, "M") // 唯一跨租户路径，产物是新命名空间 M
```

## 测试

```sh
go test ./...
```

`groupx_test.go` 用同 ID、不同载荷的交错输入（A/B 逐条交替）验证：
无跨租户去重、缓存/水位/偏移无泄漏、检查点恢复仅回滚目标租户、
未授权合并被拒绝且零副作用、授权合并产生隔离的新命名空间。
