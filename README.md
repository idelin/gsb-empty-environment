# leaseaudit — 租约争抢事件可重放审计追踪器

用于排查分布式协调故障的事件记录黑匣子。将租约的**申请、续期、移交、超时、
拒绝、竞态冲突**完整记录为带逻辑时间戳（Lamport + 向量时钟）的结构化流水，
支持按时间轴回放租约争抢全过程，并提供因果关系证据链。

仅使用 Go 标准库。

## 结构

| 文件 | 职责 |
|---|---|
| `event.go` | 事件类型、锁状态机、`Event` 结构（Seq / Lamport / 向量时钟 / CausedBy） |
| `clock.go` | 向量时钟：`HappensBefore` / `Concurrent`（竞态严格判据） |
| `tracker.go` | 黑匣子 `Tracker`：追加式日志、因果链回溯 `CausalChain` |
| `arbiter.go` | 主租约仲裁器：状态机 `IDLE→GRANTING→HELD`，授予/续期/移交/释放/超时 |
| `replay.go` | 时间轴回放 `Replay` 与审计表格渲染 `Render` |
| `cmd/demo/` | 运行样例：复现毫秒级竞态并回放审计追踪 |

## 竞态复现原理

仲裁器授予租约前存在仲裁耗时窗口（`GRANTING` 状态）。窗口内到达的第二个
申请会被拒绝，并产生 `CONFLICT` 证据事件。两份申请的向量时钟**因果并发**
（`Concurrent(a,b)==true`）即竞态的严格判据；`REJECTED`/`CONFLICT` 事件的
向量时钟同时支配两份申请，构成因果证据链。

## 运行样例

```
$ go run ./cmd/demo
=== 阶段 1: node-A 与 node-B 同时争抢主租约 ===
仲裁结果: node-A=false node-B=true
...
=== 完整审计追踪回放（时间轴） ===
SEQ  LAMPORT VECTOR                       NODE     TYPE             TRANSITION          CAUSED-BY  DETAIL
1    1       {node-B:1}                   node-B   ACQUIRE_REQUEST                                 node node-B requests lease primary
2    2       {arbiter:1 node-B:1}         arbiter  LOCK_TRANSITION  IDLE->GRANTING      #1         arbiter lock IDLE -> GRANTING
3    3       {node-A:1}                   node-A   ACQUIRE_REQUEST                                 node node-A requests lease primary
4    4       {arbiter:2 node-A:1 node-B:1} arbiter REJECTED                             #3,#1,#2   rejected node-A: lock=GRANTING holder="" term=0
5    5       {arbiter:3 node-A:1 node-B:1} arbiter CONFLICT                             #1,#3,#4   race detected: requests #1(node-B) and #3(node-A) arrived 848.75µs apart within granting window
6    6       {arbiter:4 node-A:1 node-B:1} arbiter LOCK_TRANSITION  GRANTING->HELD      #1         arbiter lock GRANTING -> HELD
7    7       {arbiter:5 node-A:1 node-B:1} arbiter ACQUIRE_GRANTED                      #1         lease granted to node-B term=1 ttl=150ms
...
```

- **到达次序**：`Seq` 1（node-B）先于 3（node-A），间隔 848.75µs（毫秒级内）。
- **锁状态变迁**：`IDLE→GRANTING→HELD` 逐步可查。
- **因果证据**：`REJECTED` 的 `CausedBy=#3,#1,#2` 指向两份申请与锁变迁；
  向量时钟证明 #1 与 #3 因果并发（竞态），#4/#5 因果晚于二者（仲裁结论）。

## 测试结果

```
$ go test -v ./...
--- PASS: TestRaceContentionEvidence (0.09s)   # 验收：竞态复现 + 证据链完整性
    两份申请到达间隔: 879.166µs (seq #1 -> #3)
--- PASS: TestRenew (0.00s)                    # 续期：持有者成功 / 非持有者拒绝
--- PASS: TestTimeoutAndReacquire (0.00s)      # 超时回收后可重新申请，任期递增
--- PASS: TestHandoff (0.00s)                  # 移交：任期递增，旧持有者失权
--- PASS: TestCausalChainAcrossLifecycle (0.00s) # 全生命周期因果链回放
--- PASS: TestVectorClock (0.00s)              # 向量时钟单元测试
PASS
ok  	leaseaudit
```
