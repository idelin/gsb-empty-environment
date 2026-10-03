# multipaxos — Multi-Paxos 多数派共识与无空洞日志提交引擎

纯 Go 标准库实现的 Multi-Paxos 核心协议状态机：Prepare/Promise 与
Accept/Accepted 两阶段、领导者租约（lease）下绕过 Phase 1 直提交、
空洞修补（gap repair）与无空洞提交（no-hole commit）。所有时间由
**单节点确定性 tick 时钟**驱动，包内不启动任何 goroutine，测试完全可复现。

## 核心不变量

- **多数派交叉**：Phase 1 需多数派 Promise；条目只有在**当前 ballot** 下被
  多数派 Accept 才可提交。两次多数派必相交，已提交条目永不丢失。
- **无空洞提交**：`commitIndex` 只沿连续前缀推进；新 Leader 在 Phase 1
  合并多数派日志（每槽取最高 ballot 的条目），对未提交槽位以原值或
  no-op 重新提议（gap repair），空洞绝不会被跳过。
- **已提交槽位不可变**：任何更高 ballot 的 Accept 都不能覆写
  `commitIndex` 之前的条目；未提交的少数派脏日志可以被安全覆写。
- **租约语义**：Leader 只有在收到多数派心跳确认后续租；租约过期自动退位，
  杜绝脑裂双主提交。租约有效期内新条目直接走 Phase 2（绕过 Phase 1）。

## 拓扑与状态转换约束

```
Follower --(选举超时)--> Candidate --(多数派 Promise)--> Leader
   ^                       |  (发现更高 ballot)             |
   +-----------------------+--------------------------------+
   Leader --(租约过期 / 发现更高 ballot)--> Follower
```

- `Ballot{N, ID}` 全序，节点 ID 打破平局，任意两节点不会产生相同 ballot。
- 消息处理在节点锁内计算出站消息、锁外投递，锁序无环，并发安全（`-race` 验证）。

## 目录结构

- `paxos/types.go` — Ballot / Entry / Role 等核心类型
- `paxos/message.go` — 六种协议消息（Prepare/Promise/Accept/Accepted/Heartbeat/Ack）
- `paxos/node.go` — 协议状态机（选举、两阶段、租约、空洞修补、日志调和）
- `paxos/network.go` — 确定性同步投递的内存网络，支持分区模拟与消息统计
- `paxos/*_test.go` — 状态机边界、分区验收场景、并发与泄漏测试
- `cmd/demo` — 可运行的 5 节点分区演示

## 运行

```sh
go test -race -count=1 ./paxos/   # 全部测试（竞态检测）
go run ./cmd/demo                 # 分区场景演示
```

## 测试覆盖

- 状态机边界：低 ballot 的 Prepare/Accept 拒绝、已提交槽不可变、未提交槽可覆写、
  单节点集群、分裂选票收敛、确定性时钟可复现、按序 apply。
- 验收场景：5 节点分区 `{0,1}|{2,3,4}`，旧 Leader 租约过期退位，多数派新 Leader
  经 Phase 1 补全未提交槽位，安全覆写少数派脏日志，愈合后全集群收敛。
- 空洞修补：注入稀疏日志（槽 1 空洞、槽 2 有值），新 Leader 以 no-op 补洞，
  提交索引连续推进。
- 并发安全：多 goroutine 并发 Propose/Tick/Snapshot（`-race`），提交集合无重复、
  无空洞、前缀一致；`runtime.NumGoroutine` 断言无 goroutine 泄漏。
