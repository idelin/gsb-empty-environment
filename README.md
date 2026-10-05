# Multi-Paxos 核心模拟（纯 Go 标准库）

固定 5 节点、多数派 3 的 Multi-Paxos 核心模拟，不依赖真实网络。

## 核心不变式

- **Ballot**：`(round, nodeID)` 全序对。
- **Acceptor 持久化**：最大 promise、每槽 accepted ballot/value（外加已决
  日志 chosen，供重启后重建应用前缀）。写盘失败时节点必须保持沉默，
  先回滚内存状态，绝不应答。
- **新主恢复**：收齐 3 份 promise 后，每槽采用响应中最高 accepted
  ballot 的值；没有已接受值的槽填 no-op。阶段一完成后才能直接提议
  后续槽。
- **Chosen**：同一 (ballot, value) 被多数派 accepted。应用只推进连续
  chosen 前缀。
- **重启**：promise/accepted 从 Store 恢复；旧 ballot 消息被拒绝。

## 模拟器

消息可丢失、重复、乱序。输入是整数 tick 加显式事件：`deliver` /
`drop` / `dup` / `crash` / `restart` / `leader` / `propose`。输出
chosen 证据（值、ballot、acceptor 集合）、应用前缀与节点状态。

独立 `Checker` 只观察 acceptor 持久化后发出的 `Accepted` 消息，断言：
任一槽不可能 chosen 两个值；任何节点 apply 的值都已 chosen。

## 运行

```sh
go test ./...                          # 全部测试
go run ./cmd/replay examples/demo.script   # 回放演示脚本
go run ./cmd/replay < myscript         # 从 stdin 回放
```

脚本命令见 `cmd/replay/main.go` 头部注释。

## 测试覆盖

- `TestMinorityOldLeader`：少数派旧主无法 chosen；其已被接受的值被新
  主保留；旧 ballot 被拒绝。
- `TestChosenButNotificationLost`：值已 chosen 但通知丢失、旧主崩溃，
  新主必须重新采用同一值。
- `TestHoleThenValue`：空洞后有值，新主填 no-op 并保留后面的值，应用
  前缀穿过空洞推进。
- `TestRepeatedRecovery`：两次恢复中途崩溃，值仍被逐任新主保留。
- `TestPersistenceFailure`：持久化失败的节点保持沉默；不足多数派时无
  法 chosen；恢复后继续推进。
- `TestRestartDurabilityAndStaleBallot`：重启保留 promise/accepted，
  拒绝旧 ballot。
- `TestSafetyChaos`：随机丢包/重复/乱序/崩溃/重启下的独立安全检查。
- `TestLiveness*`：稳定多数派 + 公平（及乱序）交付下持续推进。
