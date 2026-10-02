# Multi-Paxos 共识日志 + 分层分片局部性路由引擎 (Go)

纯标准库实现，无第三方依赖。两个独立组件：

## paxos — 多数派共识与无空洞日志提交引擎

- **两阶段协议**：Phase 1 (Prepare/Promise) 选主并收集各槽位已接受条目；
  Phase 2 (Accept/Accepted) 提交条目。
- **领导者租约优化**：选主后持有租约，连续 LogEntry 直接走 Phase 2 提交，
  跳过 Phase 1（测试中断言 `phase1 == 1`）。
- **空洞修补**：新 Leader 的 Phase 1 从 `commitIndex+1` 开始扫描，空洞槽位
  填充 no-op，多数派已接受的值按最高 term 采纳（Paxos 安全性）。
- **脏日志覆写**：反熵 Sync 消息只携带已提交条目（final），可安全覆写
  少数派上的未提交脏数据。
- **无空洞提交**：commitIndex 只在连续前缀上推进，applied 流保证无缺口。
- 同步模拟网络（`Network`）支持任意分区/恢复，无后台 goroutine，
  测试完全确定。

## shard — 分层分片与局部性感知路由

- **虚拟分片**：key 经 FNV-1a 映射到固定数量虚拟分片。
- **拓扑感知放置**：每个分片的 RF 个副本按 rendezvous (HRW) 哈希 +
  贪心约束（zone 副本最少 → rack 未占用 → 分数最高）放置，副本跨
  zone/rack 分散；节点增删时分片迁移最小化。
- **局部性路由**：`RouteLocal` 按网络距离（同节点 0 / 同机架 1 /
  同可用区 2 / 跨区 3）选择最近副本，显著降低跨节点流量。
- 放置分数使用 splitmix64 混合器（实测 FNV-1a 直接哈希有偏）。

## 运行

```sh
go test ./... -race -count=1   # 全部测试（含竞态检测）
go run ./cmd/demo              # 运行样例
```

## 验收场景

`paxos.TestPartitionNewLeaderOverwritesDirtyMinority`：5 节点集群，
{n1,n2} | {n3,n4,n5} 分区 → 旧 Leader n1 失去多数派下台（槽 3 留下脏数据）
→ n3 在多数派当选（更高 term），Phase 1 补全未提交槽位 → 恢复后反熵
同步安全覆写少数派脏日志，5 节点日志收敛一致。
