# weightedvote — 带权重投票的协同决策无冲突选择器

多方分布式加权表决状态合并器（CRDT，仅 Go 标准库）。各节点基于持股权重提交
赞成 / 反对 / 弃权，投票权重随时间因果动态调整；状态合并自动汇总最终定论，
无需任何中心协调。

## 设计

- **CRDT 状态**：每个节点持有一个 `Selector` 副本，内含两张以节点 ID 为键的
  LWW（Last-Writer-Wins）寄存器表：选票表与权重表。
- **因果排序**：每个副本维护 Lamport 时钟；本地投票 / 权重变更携带时间戳。
  合并时按 `(时间戳, 来源节点, 值)` 全序比较，过期更新永远不会覆盖新值。
- **合并性质**：`Merge` 满足交换律、结合律、幂等律 —— 任意顺序、任意次数的
  状态交换后所有副本收敛到同一状态。
- **确定性决策**：设全网有效总权重为 `T`（所有已知节点权重之和，弃权计入 `T`
  但不计入任何一方），赞成权重 `Y`、反对权重 `N`：
  - `2Y > T` → `APPROVED`
  - `2N > T` → `REJECTED`
  - 否则 → `UNDECIDED`（含恰好平票）

## 结构

- `types.go` — `Choice` / `Decision` / `Vote` / `Weight` / `Tally` 与判定规则
- `selector.go` — `Selector` 副本：投票、权重调整、合并、计票
- `selector_test.go` — 分区验收场景 + CRDT 性质测试
- `cmd/demo` — 四节点分区投票运行样例

## 运行

```sh
go test ./...    # 测试
go run ./cmd/demo # 运行样例
```

## 验收场景（selector_test.go / demo）

四节点 alpha=30, beta=20, gamma=25, delta=25（总权重 100），网络分区为
`{alpha, beta} | {gamma, delta}`：

- 分区 1：alpha 投赞成、beta 投反对，alpha 权重动态提升 30→40
- 分区 2：gamma 弃权、delta 投赞成，delta 权重动态削减 25→10
- 愈合合并后：有效总权重 95，赞成 50、反对 20、弃权 25（1 票），
  50 > 95/2 → 确定性输出 `APPROVED`，四个副本结果完全一致
