# Anti-Entropy 后台全量乱序状态修复器 (Go)

在轻量级 Gossip 之上运行低频后台反熵协程，通过 Merkle 树按深度遍历，
精确定位并补齐因概率性长尾丢包而遗漏的节点状态。仅使用 Go 标准库。

## 结构

- `store.go` — 线程安全的节点状态副本，按版本号 last-writer-wins。
- `gossip.go` — 轻量 Gossip 捎带层；`ShouldDrop(seq)` 决定丢包，
  `DeterministicDropper(10)` 确定性阻断 90% 的捎带包。
- `merkle.go` — 按节点 ID 散列到 `1<<depth` 个叶子桶，自底向上构建 Merkle 树。
- `repair.go` — 反熵修复器：自根向下逐层比对哈希，剪枝相同子树，
  仅对哈希不同的叶子桶交换条目；`Repairer.Run` 为后台周期协程，每轮双向同步。
- `cmd/demo` — 运行样例。

## 运行

```sh
go test ./... -v   # 测试
go run ./cmd/demo  # 演示：阻断 90% Gossip 包 -> 反熵补齐 2 个节点下线事件
```

## 验收场景

1. 副本 A 产生 20 个事件（含 `node-03`、`node-07` 两条下线事件，v2）。
2. Gossip 层确定性丢弃 90% 捎带包，两条下线事件恰好丢失，
   副本 B 仍认为两个节点存活（stale v1）。
3. 启动后台反熵协程后，Merkle 树分层比对仅访问 169/511 个节点，
   定位到 18 个差异叶子桶，精确补齐全部遗漏状态（含 2 条下线事件），
   双方 Merkle 根哈希一致，收敛完成。
