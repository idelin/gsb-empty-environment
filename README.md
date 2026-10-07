# retention

Go 备份保留库：按日/周/月（GFS）规则选择恢复点，并保证增量备份的
依赖链完整。

## 模型

- `Backup`：目录中的一个备份对象。`Full` 为自包含基线；`Incremental`
  通过 `ParentID` 依赖前序备份，链必须终止于某个 `Full`。
- `Graph`：依赖图。`Chain(id)` 返回从基线到该点的完整依赖链；
  链上存在缺失父节点、环或损坏（`Corrupted`）备份时返回错误。
- 恢复点可用性：链完整且无损坏才可用。**损坏依赖使恢复点不可用，
  该点会带着原因出现在报告的 `Unavailable` 字段中，绝不静默丢弃。**

## 保留策略

`Policy{Daily, Weekly, Monthly}`：对最近 N 天 / N 周 / N 月每个时间桶，
选取桶内最新的可用恢复点。`Select` 返回 `Report`：

- `Decisions`：每个备份的保留/删除决定、选择理由（如
  `daily 2026-03-01`、`weekly 2026-W09`、`dependency of d059`）与依赖链；
- `DeleteSet`：最终删除集 —— 已保留所有被选点的全部依赖；
- `Gaps`：没有任何可用恢复点的时间桶。

## 校验

`Graph.Validate(report)` 用恢复图检查报告一致性：

1. 每个备份恰好属于保留集或删除集之一；
2. 被保留的备份必须可用（链完整、无损坏）；
3. 删除集不得包含任何被保留点的依赖；
4. 被选恢复点必须携带选择理由。

## 示例

```go
g, _ := retention.NewGraph(catalog)
rep := g.Select(retention.Policy{Daily: 7, Weekly: 4, Monthly: 3}, time.Now())
if errs := g.Validate(rep); len(errs) > 0 {
    log.Fatalf("unsafe retention plan: %v", errs)
}
for _, d := range rep.Decisions {
    fmt.Println(d.Backup.ID, d.Kept, d.Reasons, d.Chain, d.Unavailable)
}
deleteBackups(rep.DeleteSet)
```

## 测试

```sh
go test ./...
```
