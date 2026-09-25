# snaprepl — 分块快照安装 + 日志追赶的单进程事件模拟

落后副本（follower）先安装**分块快照**，再重放快照点之后的日志。
通信（延迟/丢包/重复/乱序）、故障（崩溃重启）与时间（虚拟时钟）
全部在单进程事件模拟器中受控，任何场景都可由随机种子复现。
仅使用 Go 标准库。

## 结构

- `sim/sim.go` — 离散事件引擎（虚拟时钟、事件堆）与可控网络
  （延迟、丢包、重复、`Filter` 场景钩子）。
- `repl/repl.go` — 日志条目、快照元数据/分块、编解码、Leader。
- `repl/follower.go` — Follower：暂存区（staging）安装、摘要校验、
  原子发布、回滚、去重、崩溃恢复。
- `repl/checker.go` — 安全性不变量检查器。
- `main.go` — 运行样例（分区→快照追赶→污染回滚→崩溃恢复）。

## 协议要点

1. **安装期间不混读**：读路径只访问 durable 的 `applied` 状态；
   新快照先写入独立的 volatile 暂存区，校验通过后在单个模拟事件内
   一次交换（原子发布）。旧状态在安装期间始终完整可读。
2. **摘要错误回滚**：所有分块到齐后重组并校验 SHA-256；不匹配则
   丢弃整个暂存区，`applied` 状态字节不变，可重试。
3. **缺块不发布**：`got < meta.Chunks` 时永不发布。
4. **交错不重复应用**：日志条目按 `Index <= lastApplied` 跳过；
   分块按 offset 去重；快照发布后丢弃其覆盖的缓冲条目。
5. **安装后跳过已含日志**：快照 `LastIndex` 覆盖的条目一律计为
   duplicate 并跳过，状态机只前进。

## 安全性与活性证据

- **安全性**：`Checker` 在**每个事件之后**断言「follower 的 applied
  状态恒等于 leader 日志某一前缀的精确映像」。该不变量蕴含不混读、
  不重复、无空洞。样例与全部测试均 0 违例。
- **活性**：在有界丢包 + 周期重传下，测试断言 follower 在有界虚拟
  时间内追平 leader 的 commitIndex，并打印追平时刻与重传/回滚计数。

## 运行

```sh
go run .        # 运行样例，打印事件轨迹与证据
go test ./...   # 全部测试
```

## 运行样例（`go run .`，节选）

```
t=0000 phase1: leader commits while follower partitioned
t=0020 phase2: heal, send snapshot@10 + entries 13-15
t=0021 net:deliver chunk 2/3 of leader-snap-1 leader->follower
t=0023 net:deliver entry#13 leader->follower        <- 块与日志交错
t=0060 phase3: corrupted snapshot attempt
t=0067 net:deliver(dup) chunk 0/4 of leader-snap-2  <- 重复块去重
t=0080 phase3b: clean retry after rollback          <- 摘要错误已回滚
t=0122 follower crashes                             <- 暂存区丢失
t=0130 leader re-sends snapshot                     <- 重传后恢复

=== evidence ===
SAFETY: prefix invariant checked after every event: 34 checks, violations=[]
LIVENESS: follower lastApplied=15, leader commitIndex=15, caughtUp=true (t=138)
counters: applied=3 dupSkipped=0 chunks=13 chunkDup=0 staleChunk=11 rollbacks=1 snapshots=2 crashes=1
network: sent=36 delivered=27 dropped=12 duplicated=3
```

## 测试结果（`go test ./...`）

```
ok  snaprepl/repl   — TestSafetyPrefixInvariantUnderChaos   安全性：混沌交错下逐事件不变量
                      TestMissingChunkBlocksPublish         缺块不发布，旧状态不动
                      TestDigestMismatchRollback            摘要错误回滚，状态字节不变
                      TestSkipSnapshotCoveredEntries        安装后跳过快照已含日志
                      TestNoDoubleApplyInterleaved          重复块/条目不重复应用
                      TestCrashRestartRecovery              崩溃丢暂存、持久状态存活、恢复
                      TestLivenessBoundedCatchUp            活性：有界时间内追平
ok  snaprepl/sim    — TestDeterministicOrdering / TestVirtualTime
```
