# Matrix Clock 与分布式垃圾回收

纯 Go 标准库实现。每个节点维护矩阵时钟 `M[i][j]`（节点 i 所认知的、
节点 j 的最新逻辑时间），第 i 行即节点自己的向量时钟。对矩阵每列取最小值
得到 **MinVector**：`min[j]` 表示“集群中每个节点都已确认知悉节点 j 至少
推进到 `min[j]`”，以此作为安全水位回收全网已废弃的历史消息日志。

## 结构

- `clock.go` — `MatrixClock`：`Tick`（本地事件）、`Merge`（收消息：整矩阵
  取 max + 用发送方行更新自身向量时钟行 + 推进本地时钟）、`MinVector`（GC 水位）。
- `node.go` — `Node`（复制日志 + LSN）、`Cluster`（同步确定性 3 节点网络）、
  `TruncatePrefix`（物理截断已确认日志前缀）。
- `main.go` — 3 节点（A/B/C）4 轮全互联广播演示。
- `clock_test.go` / `node_test.go` — `go test` 验收。

## 运行

```sh
go run .     # 演示
go test -v ./...
```

## 运行样例（go run .）

```
通信结束后，节点 A 的日志共 16 条（LSN 1..16）

节点 A 的矩阵时钟（行=被认知的节点，列=逻辑时间分量）：
A | 16 15 15
B | 13 15 12
C | 14 15 15

GC 水位 MinVector（每个来源节点已被全网确认的最大 Seq）：A=13 B=15 C=12

判定：LSN<10 的所有事件均已被集群内每个节点接收并处理？ true
触发物理日志截断：回收 LSN 1..13 共 13 条，剩余 3 条
```

节点 A 依据矩阵时钟判定 LSN=10 之前的所有事件已被集群内每个节点完整接收，
安全触发底层物理日志截断（实际水位推进至 LSN=13）。

## 测试结果

```
=== RUN   TestTickAdvancesOwnDiagonal          --- PASS
=== RUN   TestMergeTakesElementWiseMax         --- PASS
=== RUN   TestMinVectorIsColumnMinimum         --- PASS
=== RUN   TestSnapshotIsDeepCopy               --- PASS
=== RUN   TestThreeNodeClusterGC               --- PASS  (验收场景)
=== RUN   TestNoTruncationBeforeConvergence    --- PASS
=== RUN   TestTruncateIsMonotonic              --- PASS
PASS
ok  	matrixclock
```
