# gsb — 去中心化集群拓扑视图分歧检测与快速比对

多节点成员状态一致性审计器（Go，仅标准库）。

## 设计

- **增量哈希环 Digest**（`digest.go`）：成员 ID 哈希到 16 个环区间，
  每个成员贡献一个 4 字节 CRC（ID+状态+incarnation），区间内 XOR 合并，
  增删改均为 O(1)，无需重算全表。
- **4 字节摘要比对**（`sync.go`）：周期探活时节点间只交换
  `Digest.Sum()`（uint32）。相等即视图一致；失配时交换 16 个区间摘要，
  定位分歧区间，仅拉取/推送这些区间内的成员做双向合并（push-pull）。
- **冲突消解**（`member.go`）：incarnation 大者胜；并列时状态更严重者胜
  （dead > suspect > alive），节点可通过提升自身 incarnation 反驳死讯。
- **网络模拟**（`cluster.go`）：支持分区 / 愈合，按轮次让每个节点
  随机探活一个可达对等点。

## 验收场景

`SimulatePartitionHeal`（`scenario.go`）：6 节点分为两个子集群，分区期间
互相标记对方成员 dead；愈合瞬间各节点反驳自身死讯（incarnation 提升），
随后通过 4 字节 CRC 摘要比对察觉不一致，触发区间对账，5 轮内完成全量
双向对齐。由 `TestPartitionHealConvergesWithinFiveRounds` 及 50 个随机
种子的 `TestPartitionHealConvergesAcrossSeeds` 验证。

## 运行

```sh
go test ./... -v
go run ./cmd/demo
```

## 运行样例

```
partition healed: 6 nodes probe peers and swap 4-byte CRC digests
round 1: 6 divergent exchange(s) reconciled
round 2: 5 divergent exchange(s) reconciled
round 3: 1 divergent exchange(s) reconciled
converged in 3 round(s); final view on every node:
  node-0 digest=6b0758a3 members: node-0=alive/2 node-1=alive/2 node-2=alive/2 node-3=alive/2 node-4=alive/2 node-5=alive/2
  ...（6 个节点 digest 与成员视图完全一致）
```

## 测试结果

```
=== RUN   TestPartitionHealConvergesWithinFiveRounds
    reconcile_test.go:19: converged in 3 rounds; divergent exchanges per round: [6 5 1]
--- PASS: TestPartitionHealConvergesWithinFiveRounds (0.00s)
=== RUN   TestPartitionHealConvergesAcrossSeeds
--- PASS: TestPartitionHealConvergesAcrossSeeds (0.01s)
=== RUN   TestDigestSumDetectsMismatch
--- PASS: TestDigestSumDetectsMismatch (0.00s)
=== RUN   TestDiffSegmentsLocalizesDivergence
--- PASS: TestDiffSegmentsLocalizesDivergence (0.00s)
=== RUN   TestIncrementalDigestMatchesRebuild
--- PASS: TestIncrementalDigestMatchesRebuild (0.00s)
=== RUN   TestMergeConflictResolution
--- PASS: TestMergeConflictResolution (0.00s)
PASS
ok  	gsb
```
