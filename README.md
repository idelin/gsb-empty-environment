# Anti-Entropy 状态修复器（Go 标准库实现）

在轻量级 Gossip 之上运行低频后台反熵协程，通过 Merkle 树按深度遍历比对，
精确定位并补齐因概率性丢包而遗漏的节点状态，补偿长尾丢包。

## 结构

- `state.go` — 版本化节点状态与 LWW（按版本最后写入胜出）状态存储
- `merkle.go` — Merkle 树构建（16 个叶子桶、扇出 4）与按深度分层 Diff
- `gossip.go` — 有损网络模拟（按概率丢弃捎带包）与 Gossip 节点
- `sync.go` — 反熵会话（树比对 → 定位差异桶 → 仅交换差异桶状态）与后台协程
- `antientropy_test.go` — 验收测试与单元测试
- `cmd/demo/main.go` — 端到端运行样例

## 运行

```sh
go test -v ./...     # 测试
go run ./cmd/demo    # 演示
```

## 验收场景

刻意阻断 90% 的 Gossip 捎带包后，n1/n2/n4/n5 遗失了 2 个节点下线事件
（`n2=offline@v2`、`n5=offline@v2`）。后台反熵协程启动后，通过树状分层
比对（根 → 分支 → 叶子桶，仅比较 5 个树节点即定位差异桶 `[4 11]`），
精确补齐这 2 个事件，集群收敛到同一 Merkle 根。

## 运行样例（go run ./cmd/demo）

```
=== phase 2: lossy gossip (90% packets blocked) ===
gossip loss rate: 91%
node n1 missed offline events: [n2 n5]
...
=== phase 3: Merkle tree depth-first comparison (n0 vs n1) ===
depth 0: 1 mismatched tree node(s)
depth 1: 2 mismatched tree node(s)
depth 2: 2 mismatched tree node(s)
located divergent leaf buckets: [4 11] (compared only 5 tree nodes)
=== phase 4: background anti-entropy repair ===
node n1 repaired via anti-entropy: [n2=offline@v2 n5=offline@v2]
...
all 6 nodes converged, merkle root = 9d9da158178818d4
```

## 测试结果（go test -v ./...）

```
--- PASS: TestStoreLastWriteWins
--- PASS: TestMerkleDiffLocatesDivergentBuckets  (compared 5 node pairs)
--- PASS: TestSyncStoresConvergesBothWays
--- PASS: TestAntiEntropyRepairsGossipLoss
    gossip: sent=11 dropped=10 (91% loss)
    nodes missing the offline events after gossip: [1 2 4 5]
    node n1 repaired exactly: [n2=offline@v2 n5=offline@v2]
    node n2 repaired exactly: [n2=offline@v2 n5=offline@v2]
    node n4 repaired exactly: [n2=offline@v2 n5=offline@v2]
    node n5 repaired exactly: [n2=offline@v2 n5=offline@v2]
PASS
ok  	antientropy
```
