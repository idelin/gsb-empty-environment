# raft-readindex-sim

单进程事件模拟器上的 Raft 读证据（ReadIndex）实现，仅使用 Go 标准库。
通信、故障（网络分区、崩溃）与时间（逻辑时钟）全部由模拟器控制，
不依赖任何墙钟租约（wall-clock lease）。

## 读路径协议

领导在处理读请求前必须：

1. **确认任期**：当前任期已提交至少一条日志（新领导上任先提交 no-op），
   否则拒绝读——新领导未确认任期前不能读旧状态；
2. **多数派确认**：以当前任期号向全员广播 `ReadConfirm`，收到含自己在内的
   法定多数（quorum）的 `ReadConfirmAck` 后，读索引 `readIndex = commitIndex`
   才被确认——与多数派隔离的旧领导永远凑不齐确认，无法获得读证据；
3. **等待应用**：本地状态机 `appliedIndex >= readIndex` 后才返回状态——
   已提交未应用的日志必须等应用完成。

满足以上条件后返回 `ReadEvidence{Leader, Term, ReadIndex, AppliedIndex, Acks}`，
客户端可用 `evidence.Verify(clusterSize)` 独立校验：法定多数确认 +
已应用索引覆盖读索引。

## 安全性论证（证据）

- **法定多数相交**：确认读的 quorum 与任何后续选举的 quorum 必然相交，
  且确认消息携带任期号；被隔离的旧领导无法凑齐 quorum，读要么挂起、
  要么在重新加入时下台失败，绝不返回过期状态
  （`TestIsolatedOldLeaderGetsNoEvidence`）。
- **任期确认**：新领导必须先在当前任期提交条目才能读，避免读到上一任期
  未提交的旧状态（`TestNewLeaderCannotReadBeforeTermConfirmed`）。
- **应用屏障**：证据中 `AppliedIndex >= ReadIndex`，保证读到的状态机包含
  读索引之前的全部已提交写（`TestReadWaitsForApply`、
  `TestEvidenceVerify`）。

## 活性论证（证据）

- 领导稳定且多数派可达时，读在一次确认往返（`2*NetDelay`）加上应用延迟
  内完成，测试断言有界延迟（`TestReadLivenessWithMajority`）。
- 分区愈合后，新领导立即能服务读（`TestIsolatedOldLeaderGetsNoEvidence`
  第三阶段）。

## 运行

```sh
go test -v ./...   # 安全性 / 活性证据测试
go run ./cmd/demo  # 运行样例：正常读 + 分区隔离旧领导
```

## 文件

- `sim.go` — 离散事件模拟器（逻辑时钟、事件堆）
- `raft.go` — Raft 节点：选举、日志复制、提交、异步应用
- `read.go` — ReadIndex 读路径与 `ReadEvidence`
- `raft_test.go` — 安全性与活性测试
- `cmd/demo/main.go` — 运行样例
