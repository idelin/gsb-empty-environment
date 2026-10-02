# leaseaudit — 租约争抢事件可重放审计追踪器

排查分布式协调故障的事件记录黑匣子。将租约的**申请、续期、移交、超时、
拒绝、竞态冲突**完整记录为带 Lamport 逻辑时间戳的结构化流水,支持按
时间轴回放整个争抢过程,并能从任一事件回溯因果关系证据链。仅使用 Go
标准库。

## 结构

| 文件 | 职责 |
|---|---|
| `event.go` | 事件模型、`LamportClock`、线程安全的只追加 `AuditLog` |
| `arbiter.go` | 租约仲裁器:申请/续期/移交/清扫过期,决策锁状态变迁全程审计 |
| `replay.go` | `Replay` 时间轴回放、`CausalChain` 因果链回溯、报表渲染 |
| `arbiter_test.go` | 竞态复现 + 生命周期 + 并发压力测试 |
| `cmd/demo/main.go` | 运行样例:两节点毫秒级争抢主租约 |

## 设计要点

- **到达次序先于加锁记录**:请求在争抢仲裁器决策锁*之前*就写入
  `REQUEST_ARRIVED` 事件,因此并发请求的真实到达次序被完整保留。
- **逻辑时间戳**:每个节点与仲裁器各持一个 Lamport 时钟,请求携带发送方
  时间戳,仲裁器按接收规则合并,回放时按 `(Lamport, Seq)` 全序排列。
- **因果边**:每个事件记录 `Causes`(happens-before 父事件)。竞态冲突事件
  同时指向两份到达事件;拒绝事件同时指向己方到达事件与胜出的授权事件,
  构成完整证据链。
- **fencing**:租约 term 单调递增(过期清扫不清零),过期 holder 的续期/
  移交会被 `STALE_TERM` / `NOT_HOLDER` 拒绝。

## 运行样例

```console
$ go run ./cmd/demo
=== 两个节点在毫秒级内同时争抢主租约 ===
node-A 裁决: granted=false term=1
node-B 裁决: granted=true term=1 (事件 #5)

=== 时间轴回放(逻辑时间戳排序,含仲裁器锁状态) ===
SEQ  LAM  EVENT                  NODE      LOCK   HOLDER    TERM  DETAIL
----------------------------------------------------------------------------------------------------
#1    2    REQUEST_ARRIVED        node-B    free     -         0     ACQUIRE ttl=30s
#2    3    ARBITER_LOCK_ACQUIRED  arbiter   held(#1) -         0     decision lock taken for request #1
#3    4    REQUEST_ARRIVED        node-A    held(#1) -         0     ACQUIRE ttl=30s
#4    5    RACE_CONFLICT          arbiter   held(#1) -         0     request #3 arrived while request #1 is under arbitration
#5    6    LEASE_GRANTED          arbiter   held(#1) node-B    1     granted to "node-B" until 2026-10-02T12:29:00...
#6    7    ARBITER_LOCK_RELEASED  arbiter   free     node-B    1     decision lock released after request #1
#7    8    ARBITER_LOCK_ACQUIRED  arbiter   held(#3) node-B    1     decision lock taken for request #2
#8    9    LEASE_REJECTED         arbiter   held(#3) node-B    1     rejected "node-A": LEASE_HELD_BY_OTHER (holder="node-B" term=1)
#9    10   ARBITER_LOCK_RELEASED  arbiter   free     node-B    1     decision lock released after request #3

=== node-A 被拒绝的因果关系证据链(胜者事件 #5) ===
  #1   L=2   REQUEST_ARRIVED        node=node-B   lease=master term=0 ACQUIRE ttl=30s
  #3   L=4   REQUEST_ARRIVED        node=node-A   lease=master term=0 ACQUIRE ttl=30s
  #5   L=6   LEASE_GRANTED          node=arbiter  lease=master term=1 granted to "node-B" ...
      ^-- caused by: [1]
  #8   L=9   LEASE_REJECTED         node=arbiter  lease=master term=1 rejected "node-A": LEASE_HELD_BY_OTHER ...
      ^-- caused by: [3 5]
```

(每次运行胜者可能不同——这正是竞态的本质;审计流水都能完整还原。)

## 测试结果

```console
$ go test -race -v ./...
=== RUN   TestRaceContentionAuditTrail
--- PASS: TestRaceContentionAuditTrail (0.00s)
=== RUN   TestLeaseLifecycle
--- PASS: TestLeaseLifecycle (0.00s)
=== RUN   TestConcurrentStress
--- PASS: TestConcurrentStress (0.00s)
PASS
ok  	leaseaudit	1.263s
```

- `TestRaceContentionAuditTrail`:用决策钩子确定性复现竞态——A 的请求
  被拦在仲裁临界区内,B 在此期间到达。断言:到达次序(Seq 与 Lamport
  一致且两请求到达间隔 <1ms)、恰好一次 `RACE_CONFLICT` 且其因果边指向
  两份到达事件、仲裁器锁两对获取/释放不重叠、B 的到达先于 A 的授权、
  拒绝事件的因果边为 `[B到达, A授权]`,从拒绝事件回溯的证据链完整且有序。
- `TestLeaseLifecycle`:续期、非持有者续期拒绝、过期 term 围栏、移交、
  清扫过期、过期后再申请(term 继续递增),回放终态一致。
- `TestConcurrentStress`:32 个 goroutine 真实并发争抢,`-race` 下恰好
  一个胜出,每个拒绝事件都引用胜出授权事件,回放终态单持有者且锁空闲。
