# lease — 主备租约协商状态机（单机模拟）

Go 标准库实现的主备租约管理状态机，时间以离散 tick 推进，全部事件记入
tick 事件日志。

## 设计要点

- **离散 tick 时钟**：`Cluster.Step()` 每次推进一个 tick，心跳确认与选举
  结果都在 `RoundTrip` 个 tick 后结算，所有事件带 tick 时间戳。
- **多数派续期**：主节点每 `HeartbeatEvery` tick 发心跳，只有收到多数派
  （N/2+1）应答才续租（`lease-confirmed` / `lease-renewed`）。
- **时钟漂移受界**：`Config.MaxClockDrift` 界定节点间漂移上界；
  `CanWrite` 在最坏漂移下仍保守判断租约有效性，漂移安全边界内拒绝写入。
- **主动降级防脑裂**：主节点 `StepDownTimeout` 个 tick 未获多数派应答时，
  在租约过期**之前**主动降级为候选者，保证任意时刻至多一个主。
- **多数派选主 +  fencing**：候选者获多数派承诺后当选；新主拒绝旧主的
  写/续租请求（`write-rejected`），旧主本地 `CanWrite` 亦为 false。

## 验收场景时间线

| tick | 事件 |
|------|------|
| 100  | node 0 发起租约（term 1） |
| 105  | 多数派 3/3 确认租约 |
| 139  | 注入网络黑洞，node 0 被隔离 |
| 145  | 心跳仅 1/3 可达，续租失败 |
| 150  | node 0 在租约过期（185）前主动降级为候选者 |
| 155  | node 1 以多数派 2/3 当选新主（term 2） |
| 156  | 新主拒绝旧主 node 0 的写请求 |

## 运行样例

```console
$ go run ./cmd/demo
=== event log (discrete ticks) ===
tick=100 node=0  lease-request    node 0 requests lease (term 1)
tick=105 node=0  lease-confirmed  majority 3/3 acked; lease valid until tick 155
tick=115 node=0  lease-renewed    majority 3/3 acked; lease extended to tick 165
tick=125 node=0  lease-renewed    majority 3/3 acked; lease extended to tick 175
tick=135 node=0  lease-renewed    majority 3/3 acked; lease extended to tick 185
tick=139 node=0  net-blackhole    node 0 isolated from the network
tick=145 node=0  heartbeat-failed only 1/3 reachable; no majority ack
tick=150 node=0  step-down        no majority ack for 15 ticks; demoting to candidate before lease expiry (tick 185)
tick=150 node=0  campaign         node 0 campaigns for term 2 (1 promises)
tick=150 node=1  campaign         node 1 campaigns for term 2 (2 promises)
tick=155 node=0  election-failed  node 0 got 1/3 votes; stays candidate
tick=155 node=1  elected          node 1 elected leader by majority 2/3 (term 2); lease until tick 205
tick=156 node=1  write-rejected   rejecting node 0 (term 2): lease held by node 1 (term 2)
tick=160 node=1  lease-renewed    majority 2/3 acked; lease extended to tick 210
tick=160 node=1  write-rejected   rejecting node 0 (term 2): lease held by node 1 (term 2)

final state: tick=165 leader=node 1 (term 2)
node 0 role=candidate canWrite=false (fenced, cannot write)
```

## 测试结果

```console
$ go test ./... -v
=== RUN   TestLeaseConfirmedByMajorityAt105      --- PASS  (105 多数派确认)
=== RUN   TestLeaderStepsDownAt150               --- PASS  (150 主动降级)
=== RUN   TestNewLeaderElectedByMajorityAt155    --- PASS  (155 多数派选主)
=== RUN   TestOldPrimaryRequestsRejected         --- PASS  (拒绝旧主请求)
=== RUN   TestNoMajorityNoLease                  --- PASS  (无多数派不确认租约)
=== RUN   TestBoundedClockDriftFencing           --- PASS  (漂移边界内拒绝写入)
PASS
ok  	lease
```

测试在每个 tick 上断言两个不变式：任意时刻至多一个主（无脑裂），
以及旧主在降级后任何 tick 都无法写入。

## 文件结构

- `lease.go` — 状态机核心（角色、租约、心跳、选举、事件日志）
- `lease_test.go` — 验收边界与不变式测试
- `cmd/demo/main.go` — 验收场景演示，打印完整 tick 事件日志
