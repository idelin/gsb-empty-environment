# paxosaga — Multi-Paxos 共识日志 + Saga 反向补偿编排（Go 标准库）

无单点故障的分布式共识日志协议，配合长生命周期事务的逆向补偿状态编排。
仅使用 Go 标准库。

## 结构

- `paxos/` — Multi-Paxos 多数派共识与无空洞日志提交引擎
  - Phase 1 `Prepare/Promise`：Promise 携带接受者日志，新 Leader 据此修复日志
  - Phase 2 `Accept/Accepted`：多数派接受即提交
  - Leader 租约：租约有效期内跳过 Phase 1，直接 Phase 2 提交连续 LogEntry
  - 空洞修补：Phase 1 按最高 ballot 选择槽位值，空洞填 no-op
  - 提交引擎严格按槽位顺序应用，绝不跳洞
  - 内存网络传输，支持任意分区模拟（`Partition`/`Heal`）
- `saga/` — 反向补偿与事务回滚状态编排
  - 状态机：`PENDING→RUNNING→COMPLETED / COMPENSATING→COMPENSATED / FAILED`
  - 失败时按严格逆序执行幂等补偿，指数退避重试，预算耗尽转 `FAILED`
  - 幂等键 `sagaID/step/kind`：崩溃后可安全重入，重复执行是 no-op
  - 编排器并发安全，同一 saga ID 串行化
- `cmd/demo/` — 运行样例：5 节点分区故障转移 + 转账 Saga 回滚

## 运行

```sh
go test ./... -race -count=1   # 全部测试（含竞态检测）
go run ./cmd/demo              # 运行样例
```

## 验收场景

`paxos.TestPartitionAcceptance`：5 节点集群分区 `{A,B}|{C,D,E}`，旧 Leader A
被隔离后仍持租约，其 slot 3 写入只落在少数派（脏日志，未提交）；C 在多数派
通过 Phase 1 补全未提交槽位并成为新 Leader，以新 ballot 覆写少数派脏日志；
分区愈合后全集群收敛，旧 Leader 的过期 ballot 被拒绝并退位。
