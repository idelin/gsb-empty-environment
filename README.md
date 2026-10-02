# Network Partition Merging 协调器（裂脑恢复合并引擎）

纯 Go 标准库实现。网络分裂为两个孤立子网、各自形成闭环并独立演化后，
当跨子网心跳首次打通时，安全执行**拓扑融合**与**时钟校正**，双方平滑合并、无死锁。

## 结构

```
merge/            核心库
  clock.go        Lamport 逻辑时钟（Tick / Witness 校正到 max+1）
  types.go        Member / View / 消息信封 / 确定性合并优先级键
  network.go      进程内网络模拟器：分区隔离、Heal、非阻塞投递
  node.go         节点：gossip 循环、PingReq/Ack 心跳、消息分发
  merge.go        合并握手协议（Hello / Commit 两阶段）
cmd/demo/         可运行样例：10 节点裂脑 -> 愈合 -> 合并
```

## 协议设计

1. **发现**：节点周期性向静态种子发送 `PingReq`。分区期间跨子网消息被
   网络层丢弃；重连后首个成功的 `PingReq`/`Ack` 交换发现对端
   `ClusterID` 不同，触发合并握手。
2. **Hello 阶段**：双方交换完整视图摘要（ClusterID、Epoch、成员表、时钟）。
3. **确定性发起方选举**：双方各自比较 `(Epoch, 成员数, ClusterID)` 字典序，
   键大的一方成为发起方。规则对双方一致，避免双向同时发起导致活锁。
4. **Commit 阶段**：发起方融合视图——成员取并集、Epoch 取 `max+1`、
   新 ClusterID 由两个父标识与新纪元经 FNV-1a 派生（必不同于任一父集群，
   落后节点因此总能被识别并追赶）——并向全体成员泛洪 `MergeCommit`。
5. **时钟校正**：所有消息携带 Lamport 时钟，接收方执行
   `local = max(local, remote) + 1`，合并后全网时钟快速对齐。

### 无死锁 / 无活锁保证

- 所有网络发送都是**非阻塞**的（inbox 满即丢弃并计数），且**绝不持锁发送**；
- Commit 按 Epoch 单调应用，重复投递幂等，每节点至多转发一次，泛洪必然终止；
- 落后节点（视图是超集集群的子集）由**快速路径**直接回送当前视图追赶，
  不再推进 Epoch，收敛后 Epoch 稳定、无纪元膨胀；
- 未收到 Commit 的协商按超时重发 Hello，消息丢失场景仍能最终完成。

## 运行样例

```
$ go run ./cmd/demo
=== phase 1: partition [1-5] | [6-10], evolving independently ===
--- pre-merge views (diverged) ---
  node 1  cluster=1                    epoch=2  clock=129 members=[1 2 3 4 5]
  node 2  cluster=1                    epoch=1  clock=135 members=[1 2 3 4 5]
  node 3  cluster=1                    epoch=1  clock=146 members=[1 2 3 4 5]
  node 4  cluster=1                    epoch=1  clock=143 members=[1 2 3 4 5]
  node 5  cluster=1                    epoch=1  clock=147 members=[1 2 3 4 5]
  node 6  cluster=6                    epoch=3  clock=147 members=[6 7 8 9 10]
  node 7  cluster=6                    epoch=1  clock=151 members=[6 7 8 9 10]
  node 8  cluster=6                    epoch=1  clock=135 members=[6 7 8 9 10]
  node 9  cluster=6                    epoch=1  clock=152 members=[6 7 8 9 10]
  node 10 cluster=6                    epoch=1  clock=141 members=[6 7 8 9 10]
  network: delivered=198 dropped=142 (cross-partition traffic dropped)

=== phase 2: network healed, merge handshake triggered ===
converged in 11ms: cluster=11154253388394221845 epoch=4 members=10

--- post-merge views (stable) ---
  node 1  cluster=11154253388394221845 epoch=4  clock=386 members=[1 2 3 4 5 6 7 8 9 10]
  node 2  cluster=11154253388394221845 epoch=4  clock=391 members=[1 2 3 4 5 6 7 8 9 10]
  node 3  cluster=11154253388394221845 epoch=4  clock=390 members=[1 2 3 4 5 6 7 8 9 10]
  node 4  cluster=11154253388394221845 epoch=4  clock=386 members=[1 2 3 4 5 6 7 8 9 10]
  node 5  cluster=11154253388394221845 epoch=4  clock=392 members=[1 2 3 4 5 6 7 8 9 10]
  node 6  cluster=11154253388394221845 epoch=4  clock=385 members=[1 2 3 4 5 6 7 8 9 10]
  node 7  cluster=11154253388394221845 epoch=4  clock=386 members=[1 2 3 4 5 6 7 8 9 10]
  node 8  cluster=11154253388394221845 epoch=4  clock=387 members=[1 2 3 4 5 6 7 8 9 10]
  node 9  cluster=11154253388394221845 epoch=4  clock=375 members=[1 2 3 4 5 6 7 8 9 10]
  node 10 cluster=11154253388394221845 epoch=4  clock=386 members=[1 2 3 4 5 6 7 8 9 10]
  network: delivered=677 dropped=142
  merge rounds initiated: 2
OK: split-brain healed, single merged cluster, no deadlock
```

## 测试结果

```
$ go test -v ./merge/
=== RUN   TestLamportClockCorrection      --- PASS (0.00s)   时钟校正到 max+1
=== RUN   TestMergeKeyDeterministic       --- PASS (0.00s)   发起方选举双方结论一致
=== RUN   TestDeriveClusterID             --- PASS (0.00s)   新集群标识确定性且异于父集群
=== RUN   TestPartitionIsolation          --- PASS (0.00s)   分区隔离跨组流量、愈合后恢复
=== RUN   TestMergeCommitIdempotent       --- PASS (0.00s)   Commit 重复/过期投递幂等
=== RUN   TestSplitBrainMerge             --- PASS (0.61s)   验收：10 节点裂脑合并收敛
PASS
ok      partitionmerge/merge    0.730s

$ go test -race -count=5 ./merge/        # 竞态检测 x5：通过
$ go test -run TestSplitBrainMerge -count=20 ./merge/   # 验收测试连跑 20 次：通过
```

验收测试 `TestSplitBrainMerge` 覆盖完整边界：10 节点分为 `[1-5]` 与
`[6-10]` 两组，分区期间独立演化（纪元分别推进到 2 和 3、时钟发散、
跨组流量全部被丢弃）；`Heal` 后首个跨组 `PingReq` 成功即触发握手，
5 秒内全网收敛到同一新集群标识、同一 Epoch、10 成员视图，时钟全部
校正到不低于分裂前全网最大值，收敛后 Epoch 稳定（无活锁），
`Stop` 干净退出（无死锁、无 goroutine 泄漏）。
