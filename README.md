# Matrix Clock 与分布式垃圾回收

每个节点不仅维护自己的向量时钟，还维护一个**矩阵时钟** `M[i][j]`：
"据我所知，节点 i 所认知的节点 j 的逻辑时钟"。矩阵每列的最小值
`ack[j] = min_i M[i][j]` 就是**集群内所有节点都已确认知悉**节点 j
的时间戳——这就是安全回收全网已废弃历史日志的 GC 水位线。

## 结构

- `clock.go` — `MatrixClock`：`Increment` / `Merge` / `Snapshot` / `Acknowledged`（列最小值）
- `node.go` — `Node`：本地/发送/接收事件、LSN 日志、`SafeLSN` 与 `Truncate`（物理截断）
- `hub.go` — `Hub`：同步有序消息传输，用于模拟网络
- `cmd/demo/main.go` — 3 节点（A/B/C）通信数轮后触发日志截断的可运行样例

## 核心规则

- 本地/发送事件：`M[me][me]++`
- 接收事件：发送方向量行并入自己的向量时钟，整个矩阵按元素取 max，`M[me][me]++`
- GC 水位线：`ack[j] = min_i M[i][j]`（缺失条目按 0 计——沉默节点会正确阻塞回收）
- 日志条目 `(origin=j, seq)` 满足 `seq <= ack[j]` 即可安全物理删除

## 运行样例

```
$ go run ./cmd/demo
round 1: delivered 6 messages, A's safe LSN = 0
round 2: delivered 6 messages, A's safe LSN = 2
round 3: delivered 6 messages, A's safe LSN = 7
round 4: delivered 6 messages, A's safe LSN = 12

A's matrix clock (rows: knowledge of, columns: about):
  row A: { A:20 B:17 C:17 }
  row B: { A:12 B:17 C:13 }
  row C: { A:13 B:13 C:17 }

A's GC watermark (min of each column): map[B:13 C:13 A:12]
A's log before truncation: 20 entries, LSN 1..20

==> A proves every event up to LSN 12 is acknowledged by ALL nodes
==> physical log truncated; 8 entries remain, starting at LSN 13
```

可以看到水位线逐轮收敛：每传播一轮矩阵快照，A 能证明"全网已知悉"
的前缀就向前推进，第 4 轮后 LSN ≤ 12（覆盖验收边界 LSN=10 之前的
所有事件）被安全截断。

## 测试结果

```
$ go test ./... -v
=== RUN   TestIncrementAndSnapshot
--- PASS: TestIncrementAndSnapshot (0.00s)
=== RUN   TestMergeTakesElementWiseMax
--- PASS: TestMergeTakesElementWiseMax (0.00s)
=== RUN   TestAcknowledgedIsColumnMinimum
--- PASS: TestAcknowledgedIsColumnMinimum (0.00s)
=== RUN   TestSafeLSNAndTruncate
--- PASS: TestSafeLSNAndTruncate (0.02s)
=== RUN   TestThreeNodeClusterGarbageCollection
--- PASS: TestThreeNodeClusterGarbageCollection (0.01s)
    cluster_test.go:78: A's GC watermark (acknowledged by ALL nodes): map[C:13 B:13 A:12]
    cluster_test.go:92: A physically truncated log through LSN 12; 8 entries remain (LSN 13+)
=== RUN   TestSilentNodeBlocksGarbageCollection
--- PASS: TestSilentNodeBlocksGarbageCollection (0.00s)
PASS
ok      matrixclock
```

验收场景 `TestThreeNodeClusterGarbageCollection`：3 节点通信数轮，
节点 A 依据矩阵时钟判定 LSN=10 之前的所有事件已被集群内每个节点
完整接收并处理，安全触发底层物理日志截断；同时断言第 1、2 轮时
水位线尚未到达 LSN 10（不会提前误回收）。
`TestSilentNodeBlocksGarbageCollection` 验证沉默节点会将其列的
水位线钳制为 0，保证回收安全性。
