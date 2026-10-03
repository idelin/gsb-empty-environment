# 多节点租约竞争与脑裂预防仲裁器

基于**多数派租约仲裁协议**（Quorum Lease）的主从选举实现，仅使用 Go 标准库。

## 协议设计

1. **多数派授权**：候选者向全集群请求租约授权，获得超过半数（Quorum，> N/2）授权才可成为有效 Leader；Leader 有效期取法定多数授权中最早的过期时间。
2. **授权互斥**：每个节点在租约未过期前最多授权给一个候选者（即使更高任期也不转让），因此新旧 Leader 的租约绝不重叠，任意时刻至多一个有效 Leader，从根上杜绝脑裂。
3. **自动剥离**：租约过期后主权限被自动剥离（写请求与状态查询时惰性检查），节点不再响应写请求。
4. **分区容错**：少数派分区无法凑齐多数派授权，续订/选举均失败，租约到期自动退位；多数派分区在旧租约过期后重新选举并继续服务。

## 代码结构

- `lease.go` — 协议核心：`Node`（选举/续订/写请求/授权）、`Cluster`（网络模拟与分区构造）、`Clock`（可注入时钟）
- `lease_test.go` — `go test` 验收测试（FakeClock 保证确定性）
- `cmd/demo/main.go` — 真实时钟的运行演示

## 运行测试

```sh
go test -v ./...
```

测试结果：

```
=== RUN   TestMajorityElection
--- PASS: TestMajorityElection (0.00s)
=== RUN   TestMinorityPartitionCannotElect
--- PASS: TestMinorityPartitionCannotElect (0.00s)
=== RUN   TestGrantExclusivityUntilExpiry
--- PASS: TestGrantExclusivityUntilExpiry (0.00s)
=== RUN   TestLeaseRenewal
--- PASS: TestLeaseRenewal (0.00s)
=== RUN   TestSplitBrainPrevention
--- PASS: TestSplitBrainPrevention (0.00s)
=== RUN   TestNonLeaderRejectsWrites
--- PASS: TestNonLeaderRejectsWrites (0.00s)
=== RUN   TestHealDoesNotResurrectOldLeader
--- PASS: TestHealDoesNotResurrectOldLeader (0.00s)
PASS
ok  	lease-arbiter	0.565s
```

其中 `TestSplitBrainPrevention` 对应验收边界：5 节点集群分区为 2+3，
2 节点分区的旧 Leader 续订失败、租约过期后自动剥离主权限并拒绝写；
3 节点分区在旧租约过期后重新选举并继续服务；全程至多一个有效 Leader。

## 运行演示

```sh
go run ./cmd/demo
```

运行样例（实际输出）：

```
== 1. 全连通，node1 发起选举 ==
  node1 获得 5/5 租约授权，成为 Leader
  状态: [node1:LEADER] [node2:-] [node3:-] [node4:-] [node5:-]
  write set a=1                -> node1 ACCEPTED

== 2. 后台每 500ms 续订租约，随后发生分区 {1,2} | {3,4,5} ==
  已分区。node1 在 2 节点分区中续订只能拿到 2/5 授权 -> 失败

== 3. 租约有效期内：多数派分区选举仍被授权互斥阻止 ==
  node4 选举失败（预期内）: no quorum: majority lease grants unavailable (grants=0, need>2)
  状态: [node1:LEADER] [node2:-] [node3:-] [node4:-] [node5:-]

== 4. 等待租约过期（2s）... ==
  状态: [node1:-] [node2:-] [node3:-] [node4:-] [node5:-]  <- node1 租约过期，主权限自动剥离
  write set b=2                -> node1 REJECTED: not leader: write rejected

== 5. 多数派分区重新选举 ==
  node4 获得 3/5 租约授权，成为新 Leader
  状态: [node1:-] [node2:-] [node3:-] [node4:LEADER] [node5:-]
  write set b=2                -> node4 ACCEPTED
  write set c=3                -> node1 REJECTED: not leader: write rejected

== 6. 恢复分区：旧 Leader 不会复活 ==
  状态: [node1:-] [node2:-] [node3:-] [node4:LEADER] [node5:-]
  node1 日志: [set a=1]（分区后未接受任何写）
  node4 日志: [set b=2]
```

> 注：协议假设集群节点时钟同步（租约安全性的标准前提）；测试通过共享 FakeClock 严格满足，演示使用本机真实时钟。
