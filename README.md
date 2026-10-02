# Lease Read —— 基于租约的多副本强一致读优化器

分布式数据库只读优化技术（Go 标准库实现，无第三方依赖）。

## 原理

主节点通过**多数派心跳**获取租约（Lease）：在租约有效期内，多数派从节点承诺
不发起新选举、不接受新主。因此主节点可以确信本地副本仍然是最新的，只读请求
**无需逐请求网络协商**，直接本地读取即可保证**严格线性一致性**。

安全性依赖时钟漂移上界：租约过期时间扣除安全余量（max clock drift）后，
即为"绝对安全窗口"。

```
读请求
  │
  ├─ 租约剩余安全时间 ≥ 100ms ──► 本地读，零网络开销（快路径）
  │
  └─ 租约剩余安全时间 < 100ms ──► 自动降级：多数派网络确认读（慢路径）
```

## 代码结构

- `lease.go` — `LeaseManager`：租约授予/续期/安全窗口判定（100ms 降级阈值）
- `optimizer.go` — `ReadOptimizer`：读路径分流（本地读 / 多数派确认读）、写路径多数派复制
- `cluster.go` — `Transport` 抽象与 `SimTransport`（模拟网络延迟 + RPC 计数，用于验证零网络开销）
- `store.go` — 版本化 KV 存储（版本号保证线性一致性比较）
- `optimizer_test.go` — 验收测试
- `cmd/demo/main.go` — 运行样例

## 运行

```bash
go test ./... -v     # 验收测试
go run ./cmd/demo    # 运行样例
```

## 验收结果

### 测试（go test）

```
=== RUN   TestLeaseReadZeroNetwork
    10000 concurrent lease reads finished in 7.6ms (1309008 reads/s), network calls on read path: 0
--- PASS: TestLeaseReadZeroNetwork (0.01s)
=== RUN   TestDegradeWhenLeaseBelowThreshold
    degraded path: 100 reads, 200 fetch RPCs (quorum confirmation)
--- PASS: TestDegradeWhenLeaseBelowThreshold (0.18s)
=== RUN   TestLeaseRenewalRestoresFastPath
--- PASS: TestLeaseRenewalRestoresFastPath (0.01s)
=== RUN   TestLinearizableUnderConcurrentWrites
--- PASS: TestLinearizableUnderConcurrentWrites (0.03s)
=== RUN   TestNoLeaseNoLocalRead
--- PASS: TestNoLeaseNoLocalRead (0.00s)
PASS
ok      leaseread
```

### 运行样例（go run ./cmd/demo）

```
=== 阶段 0：主节点通过多数派心跳获取 5s 租约 ===
租约已授予，剩余安全窗口 ≈ 4.99s（心跳 RPC: 2 次）

=== 阶段 1：租约安全窗口内，10,000 次并发读 ===
完成 10000 次读，耗时 5ms，吞吐 1994913 reads/s
本地读: 10000，降级读: 0，读路径网络调用: 0（零网络开销 ✓）

=== 阶段 2：租约剩余不足 100ms，自动降级 ===
剩余安全窗口 ≈ 40ms < 100ms 阈值
读结果: {Value:latest-value Version:1}（走多数派网络确认，Fetch RPC: 2 次）

=== 阶段 3：续租后恢复零网络快路径 ===
续租成功，读路径网络调用: 0（恢复本地读 ✓）
```

## 验收边界对照

| 验收项 | 结果 |
|---|---|
| 1 万次并发读 | ✅ 10,000 goroutine 并发读全部成功，~200 万 reads/s |
| 租约绝对安全窗口内零网络开销 | ✅ 读路径网络调用计数为 0（`SimTransport` 计数器断言） |
| 剩余 < 100ms 自动降级 | ✅ 降级为多数派 Fetch 确认读，结果仍线性一致 |
| 严格线性一致性 | ✅ 并发写测试：读永远返回最高已提交版本；无租约绝不本地读（防脑裂脏读） |
