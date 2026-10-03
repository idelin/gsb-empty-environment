# Multi-Paxos 多数派共识与无空洞日志提交引擎（Go 标准库实现）

无单点故障的分布式共识日志协议，含 Prepare/Promise 与 Accept/Accepted
两阶段优化、领导者租约快速路径、空洞修补，以及细粒度度量监控与统计聚合导出。
仅使用 Go 标准库。

## 结构

- `paxos/types.go` — Ballot / Entry / Promise / 错误码（`ErrCode`）
- `paxos/node.go` — Multi-Paxos 节点：Phase 1（Prepare/Promise）、
  Phase 2（Accept/Accepted）、租约快速路径、Phase 1 空洞修补、
  Commit 广播与缺口追赶（Fetch）
- `paxos/transport.go` — 内存传输层：确定性网络分区、模拟延迟、丢包统计
- `paxos/metrics.go` — 计数器、错误码、命中率、精确百分位延迟直方图、
  有界高精度（纳秒）时序环形缓冲；全部并发安全，无后台 goroutine
- `paxos/export.go` — 节点快照、跨节点聚合（计数求和 / 命中率加权 /
  直方图样本池化重算分位）、JSON 导出
- `paxos/cluster.go` — 集群辅助与聚合入口
- `cmd/demo/main.go` — 端到端运行样例

## 关键设计

- **两阶段优化**：租约有效期内，领导者对连续 LogEntry 直接走 Phase 2
  提交（`lease` 命中率统计）；租约过期或首次提案自动重入 Phase 1。
- **无空洞提交**：新领导者 Phase 1 收集多数派的 accepted/committed 状态，
  对 `(commitIndex, maxSeen]` 内每个槽位重提最高 ballot 的值，无人持有的
  槽位填 noop，保证提交前缀连续。
- **安全覆写脏日志**：Commit 广播 + 缺口 Fetch 使少数派未提交的脏值被
  已提交值覆写（`dirtylog.overwrite` 计数）；已提交值永不被改写
  （`CONFLICT_COMMIT` / `STALE_COMMIT` 错误码恒为 0）。

## 运行样例

```
$ go run ./cmd/demo
== leader elected: node 0 ==
committed 5 entries, lease hit rate on leader: 1.00
== partition: {0,1} | {2,3,4} ==
old leader propose fails as expected: NO_QUORUM: accept slot 6: 2/3 votes
== new leader elected: node 2 (repairs holes via Phase 1) ==
== partition healed ==
node 0: commitIndex=7 applied=7 dirty-overwrites=1
node 1: commitIndex=7 applied=7 dirty-overwrites=1
node 2: commitIndex=7 applied=7 dirty-overwrites=0
node 3: commitIndex=7 applied=7 dirty-overwrites=0
node 4: commitIndex=7 applied=7 dirty-overwrites=0
== aggregated cluster metrics ==
{
  "nodes": 5,
  "counters": { "accept.ok": 33, "propose.ok": 7, "dirtylog.overwrite": 2, ... },
  "errors": { "NO_QUORUM": 1, "PARTITION": 9 },
  "hitRates": { "lease": 1 },
  "histograms": { "paxos.propose.e2e": { "count": 7, "p50": ..., "p99": ... }, ... }
}
```

## 测试结果

```
$ go test ./... -count=1
ok  	paxos/paxos	0.990s   # 22 个测试全部通过
$ go test ./... -race -count=1
ok  	paxos/paxos	9.454s   # 竞态检测通过
```

覆盖：选举与租约快速路径、租约过期重入 Phase 1、5 节点分区下旧 Leader
被多数派取代 + Phase 1 补全未提交槽位（noop 空洞 + 值收养）+ 少数派脏
日志安全覆写、已提交值跨 Leader 保留、并发提案/竞选一致性、直方图
分位/容量/空/单样本边界、命中率边界、错误码统计、时序环形缓冲与
纳秒时间戳、快照隔离、聚合与 JSON 导出、Metrics 全 API 并发安全、
严格无 goroutine 泄漏。
