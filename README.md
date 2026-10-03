# crdtmove — 树形文件系统的无环 CRDT 移动/重命名协调器 (Go)

多节点分布式虚拟目录树的移动协调：仅使用 Go 标准库，防止并发互移
（A 移入 B 且 B 移入 A）产生孤立环路，目录不丢失、各副本确定性收敛。

## 设计

- **操作日志 + Lamport 时间戳**：每次 `Move`/`MoveRename` 生成
  `OpID{Lamport, Replica}`，构成确定性全序；`Merge` 时按
  `clock = max(local, remote)` 因果推进。
- **确定性重放仲裁**：合并后按 OpID 全序重放整个日志重建状态。
  重放是纯粹的全序函数，因此合并满足幂等、可交换、可结合，
  所有副本收敛到同一拓扑。
- **父节点因果追溯断环**：应用每个操作时，沿当前父链从目标父节点
  上溯，若遇到被移动节点（即目标位于其子树内）则成环，仲裁撤销
  该操作——全序中较晚者被撤销，先者生效。同目录重名冲突同理。
- **无丢失保证**：被撤销的只是移动操作本身，节点始终保留在树中；
  `IsAcyclic()` 可校验任意时刻每个节点都能沿父链到达根。

## 结构

- `crdt/tree.go` — 核心 CRDT：`Tree`、`MoveOp`、`OpID`、`Merge`、重放仲裁
- `crdt/tree_test.go` — `go test` 验收测试（含并发互移断环场景）
- `cmd/demo/main.go` — 运行样例：两节点并发互移 /dirA、/dirB 后合并

## 运行

```sh
go test ./... -v   # 测试
go run ./cmd/demo  # 演示
```

## 验收场景结果

节点1 `move(dirA -> dirB, 1@node1)` 与节点2 `move(dirB -> dirA, 1@node2)`
并发提交、双向合并后：

- 检测到拓扑依赖环，按全序 `1@node1 < 1@node2` 撤销后发生的 `1@node2`；
- 两副本收敛为 `/dirB/dirA`，无环、dirA/dirB 均无丢失。
