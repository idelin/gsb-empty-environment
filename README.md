# backupretention

Go 备份保留库：按日 / 周 / 月（GFS）规则选择恢复点，结合恢复图
（recovery graph）推导出安全的删除集。

## 核心语义

- **GFS 选择**：`Policy{Daily, Weekly, Monthly}` 对每个粒度，在最近 N 个
  桶（自然日 / ISO 周 / 自然月）中各保留最新的恢复点；同一命中多个规则
  的点会累积多条选择理由。
- **依赖闭包**：增量备份通过 `DependsOn` 声明对全量基线与前序增量的依赖。
  删除集 = 全部点 − 所选点的传递依赖闭包，因此所选点的全部依赖必然保留；
  仅作为依赖被保留的点列入 `Protected`。
- **损坏即不可用，而非静默丢弃**：依赖链上任何一点损坏（或缺失）时，
  依赖它的恢复点被标记为 `UNUSABLE` 并附带问题说明，仍保留在选择报告
  与依赖闭包中——删除是破坏性操作，库选择保守保留并显式报告。
- **恢复图校验**：`Graph.Validate()` 检查缺失依赖、依赖时间不早于
  依赖方、全量带依赖 / 增量无依赖、依赖环、损坏点。

## 用法

```go
g, err := retention.NewGraph(catalog) // []retention.Point
if err != nil { ... }

rep := retention.BuildReport(g, retention.Policy{
    Daily: 7, Weekly: 4, Monthly: 3,
}, time.Now())

fmt.Println(rep) // 选择理由、依赖链、可用性、保护集、删除集、图问题
for _, p := range rep.DeleteSet {
    deleteBackup(p.ID)
}
```

输出示例（节选）：

```
== Selected recovery points ==
  incr-2026-08-16  2026-08-16 03:00  type=incremental
    reasons: daily:2026-08-16, weekly:2026-W33
    chain:   full-2026-08-10(full) -> incr-2026-08-11(incremental) -> ... -> incr-2026-08-16(incremental)
    status:  UNUSABLE
      - via "incr-2026-08-16": "incr-2026-08-15" is corrupt: checksum mismatch
```

## 测试

```sh
go test ./...
```
