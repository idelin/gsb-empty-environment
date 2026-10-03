# Multi-Paxos 多数派共识与无空洞日志提交引擎（Go 标准库实现）

无单点故障的分布式共识日志协议，仅依赖 Go 标准库。

## 特性

- **两阶段协议**：Phase 1 `Prepare/Promise` + Phase 2 `Accept/Accepted`，Ballot 全序（编号 + 节点 ID 打破平局）。
- **领导者租约**：Phase 1 成功后获得租约，租约期内连续 `LogEntry` 直接走 Phase 2 提交，跳过 Phase 1；Follower 在租约期内拒绝其他领导者的 `Prepare`。
- **无空洞日志提交**：新领导者通过 Phase 1 收集多数派的已接受条目，空洞槽位填充 noop，已被接受的值按最高 Ballot 采纳后重放，提交水位线连续推进。
- **少数派脏日志安全覆写**：已提交槽位不可变；未提交的脏槽位在新领导者的 Accept/Commit 到达时被安全覆盖。
- **并发安全**：所有状态由互斥锁保护，持锁期间不做网络调用（锁序为 DAG，无死锁）；广播 goroutine 一律 `WaitGroup` 汇合，严格无泄漏；`-race` 通过。

## 结构

- `ballot.go` — Ballot 全序、日志条目与 RPC 消息
- `node.go` — Acceptor/Learner 处理器 + Proposer（租约快速路径与完整竞选）
- `network.go` — 支持分区编程的内存传输层
- `paxos_test.go` — 并发竞争、分区仲裁、空洞修补、泄漏断言等测试
- `cmd/demo/main.go` — 5 节点集群运行样例

## 运行

```sh
go test ./... -race -v   # 测试（含竞态检测）
go run ./cmd/demo        # 运行样例
```

## 验收场景

`TestPartitionOldLeaderReplaced` 覆盖验收边界：5 节点集群中旧 Leader 被分区到少数派 `{0,1}`，多数派 `{2,3,4}` 在租约过期后选出新 Leader；新 Leader 通过 Phase 1 将未提交槽位补为 noop 并继续提交；分区恢复后旧 Leader 的过期 Ballot 被拒绝，少数派上的脏日志被安全覆写，全集群收敛到同一条无空洞日志。
