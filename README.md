# partitionmerge — 网络分区自愈（裂脑恢复合并）协调器

纯 Go 标准库实现的 split-brain 恢复合并引擎。网络分裂为两个孤立子网、各自
形成闭环并独立演化后，当跨子网心跳首次打通（首个成功的 `PingReq`）时，安全
执行拓扑融合与 Lamport 时钟校正，平滑合并、无死锁。

## 设计

- **节点**（`node.go`）：每个节点持有 `clusterID`、`epoch`、Lamport 时钟和
  成员表（成员 ID → 已知最高心跳）。所有状态由互斥锁保护，且**持锁期间绝不
  发送网络消息**——消息处理器返回待发送列表，解锁后再投递，从根上消除锁序
  死锁。
- **网络**（`network.go`）：模拟交换机，支持 `Partition(a, b)`（双向丢弃跨组
  流量）与 `Heal()`。投递为异步缓冲信箱，模拟真实数据报网络（可丢包）。
- **闭环形成**：`FormCluster` 把一组节点闭合成环（每节点播种后继节点），随后
  gossip 在分区内部传播成员表，两个分区各自独立演化。

## 合并握手协议

```
节点1 (cluster-1)                节点6 (cluster-6)
     | ---- PingReq{digest} ------> |   跨分区首包打通
     | <----- Ack{digest} --------- |
     |  检测到异构 clusterID；       |
     |  cluster-1 < cluster-6，      |
     |  由 cluster-1 侧唯一驱动合并   |
     | -- MergeSync{epoch=2, ∪成员} -->  (洪泛到两侧全体成员)
     | <---- MergeDone ------------- |
     |  接收方应用后按 epidemic 转发，   |
     |  仅在更高 epoch 时转发一次 → 收敛  |
```

关键规则：

1. **触发**：任何消息（`Ping`/`PingReq`/`Ack`）携带的 `clusterID` 与本地不同
   且对方 epoch 不落后时，视为发现异构集群。
2. **唯一驱动者（确定性破对称）**：只有 `clusterID` 字典序较小的一侧驱动合并，
   避免两侧同时发起而互相等待（死锁）。
3. **拓扑融合**：新 epoch = `max(epochA, epochB) + 1`；成员表取两侧并集；
   `MergeSync` 幂等，仅在更高 epoch 时应用并转发，保证收敛且不产生消息风暴。
4. **时钟校正**：全程 Lamport 规则 `L = max(L_local, L_msg) + 1`；合并洪泛 +
   gossip 使所有节点时钟收敛到超过分裂前两侧的最大值。

## 运行

```sh
go run ./cmd/demo     # 运行样例：分区 → 独立演化 → 重连 → PingReq 触发合并
go test ./...         # 全部测试
go test -race ./...   # 竞态检测（含并发合并测试）
```

## 测试（`merge_test.go`）

- `TestPartitionMerge`：验收场景。10 节点分 [1-5]/[6-10] 各自演化；分区期间
  跨组 `PingReq` 被丢弃；愈合后首个 `PingReq` 成功触发合并握手；断言全体收敛到
  `cluster-1`、epoch=2、成员表 10 节点、Lamport 时钟全部超过分裂前最大值。
- `TestSingleDriver`：大 ID 集群先探测时仍只有小 ID 侧驱动，epoch 恰好 +1。
- `TestMergeIdempotent`：合并后的后续探测幂等，epoch 不再变化。
- `TestConcurrentMerge`：真实 goroutine + 定时器跑全流程，配合 `-race` 验证
  无数据竞争、无死锁（5 秒收敛超时兜底）。
