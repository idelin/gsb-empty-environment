# barrieragg — 屏障代际聚合器

聚合器对多个生产分区发起重置（reset barrier）：屏障前的事件计入旧代，
屏障后的事件计入新代。各分区乱序到达屏障，每次到达被分别缓存；
只有全部存活分区到齐，才发布旧代终值并滚动到新代。

## 结构

- `agg/event.go` — 事件标签模型：`control / data / barrier / membership / publish`
  五类标签，全部决策追加到只增日志。
- `agg/aggregator.go` — 状态机。状态是日志的纯折叠（`applyEvent`），
  实时路径与 `Replay` 共用同一折叠函数，恢复正确性由构造保证。
- `agg/verify.go` — 独立核对器，只依据带标签的日志重放并检查不变量。
- `cmd/sim` — 场景模拟：乱序到屏障、重复屏障、分区退出、崩溃恢复。

## 核对的不变量

1. **exactly-once** — 同一世代只发布一次终值；
2. **completeness** — 发布时所有存活成员均已越过屏障；
3. **no-duplicates** — 同一重置窗口内同一分区只越障一次
   （重复屏障记 `BarrierIgnored/duplicate`）；
4. **exit-safety** — 分区退出后其数据与迟到越障均被忽略，
   待达集合随之收缩，屏障可因此完成；
5. **value-safety** — 终值等于该代被接受的旧代事件之和；
6. **carry-safety** — 滚动带入值等于已缓存的新代事件之和；
7. **timeliness** — 集合完成的下一事件即为发布，发布紧随滚动。

## 恢复

`Restore(log)` 从持久化日志重放状态。测试对每个崩溃点断言
`Replay(log) == 实时状态`，并对随机操作序列做模糊核对。

## 运行

```sh
go test ./...
go run ./cmd/sim
```
