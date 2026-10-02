# Multi-Paxos 共识日志 + Saga 反向补偿事务编排 (Go)

纯标准库实现，无第三方依赖。两个独立包：

## paxos — 多数派共识与无空洞日志提交引擎

- **两阶段协议**：`Prepare/Promise`（Phase 1）与 `Accept/Accepted`（Phase 2）。
- **领导者租约**：`Elect` 成功后持有租约，租约期内 `Propose` 直接走 Phase 2
  提交连续 LogEntry，跳过 Phase 1（测试用 RPC 计数器断言零 Prepare）。
- **无空洞提交**：`commitIndex` 只沿连续已提交前缀推进；新 Leader 当选后先对
  `commitIndex+1 .. maxSlot` 做修复——多数派已接受的槽位重选最高 term 的值，
  无人接受的槽位填 no-op，然后才接受新写流量。
- **脏日志安全覆写**：少数派上未提交的条目在新 term 的 Accept 到达时被覆写；
  已提交值由多数派交集保护，换主后必然被新 Leader 重新选定。
- **分区模拟**：`Network` 支持任意双向分区与恢复，同步投递保证测试确定性。

## saga — 反向补偿与事务回滚状态编排

- 步骤顺序执行，任一步失败即按**严格逆序**补偿已完成的步骤。
- **幂等**：同一事务 ID 重复 `Execute` 不产生重复副作用；已完成返回 nil，
  已回滚返回原始错误。
- **最终一致**：补偿失败按 `MaxCompensateRetries` + 退避重试；耗尽后进入
  `CompensationStuck`，可经 `Resume` 继续直至回滚完成。
- 状态机：`Pending → Running → Completed | Compensating → RolledBack |
  CompensationStuck`，全部状态转换由每事务互斥锁保护，并发安全、无后台
  goroutine（测试断言无泄漏）。

## 运行

```sh
go test -race -count=1 ./...   # 全部测试（含竞态检测）
go run ./cmd/demo              # 分区换主 + 订单回滚演示
```

## 验收场景（paxos.TestPartitionOldLeaderReplacedAndDirtyLogOverwritten）

1. 5 节点集群，node0 当选 term 1，提交槽位 1-2；
2. 分区 `{0,1}|{2,3,4}`，旧 Leader 的槽位 3 只到达少数派 → 脏而未提交；
3. 短暂恢复后槽位 4 在全员提交，槽位 3 成为真正的空洞；
4. 再次分区，node2 以 term 2 在多数派当选：Phase 1 发现空洞并填 no-op、
   重选槽位 4 的已选值，随后在租约下直接提交槽位 5；
5. 恢复后旧 Leader 的过期写入被拒并退位，`SyncFollowers` 用 term 2 的值
   覆写少数派脏日志；
6. 断言全部 5 节点收敛为 `[set-user, set-config, noop, set-order,
   set-payment]`，`commitIndex=5`，无空洞、无脏值泄漏。
