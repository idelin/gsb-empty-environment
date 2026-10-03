# paxosshard

Multi-Paxos 多数派共识 + 无空洞日志提交引擎，以及分层分片与局部性感知路由引擎。纯 Go 标准库实现。

## 模块

### `paxos/` — Multi-Paxos 共识日志
- **两阶段协议**：Phase 1（Prepare/Promise）选主并学习多数派已接受值；Phase 2（Accept/Accepted）逐槽位提交，多数派接受即提交并广播 Commit。
- **领导者租约**：租约有效期内 Leader 跳过 Phase 1，直接以 Phase 2 连续追加 `LogEntry`；持有租约的 Follower 拒绝其他 Ballot 拥有者的 Prepare/Accept。
- **无空洞提交**：`commitIndex` 只沿连续已提交前缀推进；新 Leader 在 Phase 1 修补 `(commitIndex, maxSlot]` 内所有槽位——最高 Ballot 的被接受值胜出，空洞填 no-op。
- **脏日志安全覆写**：分区愈合后，心跳携带 Follower 的 commitIndex 与脏槽位清单，Leader 重放缺失提交并对未提交冲突槽位写入已选值或 no-op，少数派脏日志被安全覆写。
- 无后台 goroutine、无单点故障；所有状态由互斥锁保护，`-race` 通过。

### `shard/` — 分层分片与局部性感知路由
- **虚拟分片**：键空间固定映射到 N 个 vshard，键 → vshard 与成员变更解耦。
- **加权 rendezvous（HRW）放置**：指数竞速采样（`-ln(u)/weight`），节点增删只迁移受影响分片（实测单节点加入迁移 ~8%，接近理论值 1/13）；`Capacity` 作为权重。
- **故障域多样性**：副本贪心分散到不同 Zone，再到不同 Region。
- **局部性路由**：`RouteLocal` 在副本集中选择拓扑距离最近者（同 Zone < 同 Region < 跨区域），显著降低跨节点通信；`Stats` 提供平均路由距离与主副本分布度量。

## 运行

```sh
go test ./...          # 全部测试（建议 -race）
go run ./examples/demo # 运行样例：分区切换演示 + 路由局部性统计
```

## 验收场景（`paxos/node_test.go`）
`TestPartitionLeaderReplacement`：5 节点集群，旧 Leader 被分区至少数派 {0,1}，多数派 {2,3,4} 选出新 Leader；新 Leader 经 Phase 1 补全未提交槽位，愈合后心跳修复安全覆写少数派脏日志，全集群日志严格一致。
