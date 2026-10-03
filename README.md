# Lease Read Optimizer（基于租约的多副本强一致读优化器）

分布式数据库只读优化技术（Go 标准库实现，无第三方依赖）。主节点在租约
绝对安全窗口内直接本地读取最新已提交数据，零网络开销，保证严格线性一致性；
租约剩余不足 100ms 时自动降级为全量网络心跳确认（read-index 风格）。

## 设计

```
                Read(key)
                   │
        remaining := lease.Expiry - now
                   │
     ┌─────────────┴──────────────┐
 remaining >= 100ms          remaining < 100ms
 （绝对安全窗口）              （降级窗口）
     │                            │
 本地状态机直接读取         并行心跳全部 follower
 零网络往返                 多数派确认 → 续约 → 本地读取
                            无多数派 → ErrNoQuorum（拒绝脏读）
```

- **线性一致性依据**：租约由多数派心跳授予，租约有效期内不可能存在另一个
  主节点提交更新的写，因此主节点本地 applied 状态即最新已提交状态。
- **租约维护**：后台每 `leaseDuration/2` 发起一轮多数派心跳续约，成本被
  摊销，与读请求数量无关。
- **降级路径**：剩余租约 < 100ms 时，每次读先做一次全量 quorum 心跳确认
  （同时完成续约），确认成功才本地返回；多数派不可达时拒绝服务而非返回
  过期数据。

## 代码结构

- `leaseread/lease.go` — 租约（授予/续约/剩余时间）
- `leaseread/optimizer.go` — 核心优化器：快速路径 + 降级路径 + 指标
- `leaseread/transport.go` — 网络抽象（每次心跳 = 一次网络往返，可计数）
- `leaseread/store.go` — 已提交状态机（带 applied index 的 KV）
- `leaseread/cluster.go` — 模拟集群（主 + 从 + 后台续约）
- `cmd/demo/main.go` — 运行样例

## 运行样例

```
$ go run ./cmd/demo
=== Lease Read Optimizer Demo ===
cluster: 1 leader + 2 followers, lease=1s, degrade threshold=100ms

--- Phase 1: lease inside absolute safe window ---
concurrent reads      : 10000
served locally        : 10000 (zero network overhead)
degraded reads        : 0
network rounds (reads): 0
total network rounds  : 4 (background lease renewals x2)
elapsed               : 13.435ms
throughput            : 744322 reads/sec

--- Phase 2: lease remaining < 100ms, auto-degrade ---
lease remaining       : 20ms
read result           : {"name":"ada","balance":100}
quorum heartbeats     : 2 network rounds for this single read
lease renewed to      : 1s remaining

--- Phase 3: quorum lost ---
read with no quorum   : err=leaseread: cannot confirm quorum (stale read rejected)
```

## 测试结果

```
$ go test ./... -v        # 亦通过 go test -race ./...
=== RUN   TestConcurrentLocalReadsZeroNetwork
--- PASS: TestConcurrentLocalReadsZeroNetwork (0.46s)
    optimizer_test.go:74: 10000 concurrent lease reads in 434.9ms, 0 network rounds
=== RUN   TestDegradeBelowThreshold
--- PASS: TestDegradeBelowThreshold (0.00s)
=== RUN   TestDegradeThresholdBoundary
--- PASS: TestDegradeThresholdBoundary (0.00s)
=== RUN   TestNoQuorumRefusesRead
--- PASS: TestNoQuorumRefusesRead (0.00s)
=== RUN   TestExpiredLeaseNeverServesLocally
--- PASS: TestExpiredLeaseNeverServesLocally (0.00s)
=== RUN   TestLinearizableReadYourWrites
--- PASS: TestLinearizableReadYourWrites (0.00s)
=== RUN   TestClusterWithRenewalSustainsLocalReads
--- PASS: TestClusterWithRenewalSustainsLocalReads (0.20s)
PASS
ok      leaseread/leaseread
```

| 测试 | 验证点 |
|---|---|
| `TestConcurrentLocalReadsZeroNetwork` | 验收 1+2：1 万并发读，安全窗口内全部本地返回，0 次网络往返 |
| `TestDegradeBelowThreshold` | 验收 3：剩余 < 100ms 自动降级为全量心跳确认，并顺带续约 |
| `TestDegradeThresholdBoundary` | 边界：恰好 100ms 走快速路径，99ms 走降级路径 |
| `TestNoQuorumRefusesRead` | 多数派不可达时拒绝读，绝不返回过期数据 |
| `TestExpiredLeaseNeverServesLocally` | 租约过期后绝不走本地快速路径 |
| `TestLinearizableReadYourWrites` | 并发写后读，永远读到最新已提交值 |
| `TestClusterWithRenewalSustainsLocalReads` | 后台续约下持续 1 万并发读仍零网络开销 |
