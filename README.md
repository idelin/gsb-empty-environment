# twopc — 可回退两阶段提交协调器

离散事件环境下的 2PC 协调器（Go，仅标准库）。协调器的每一次状态转移都先
写入预写式日志（WAL）再执行外部动作，崩溃重启后重放 WAL、向在线参与者
查询状态并完成决议，保证不产生孤儿事务。

## 设计

- **WAL 优先**：`BEGIN → VOTE* → DECISION → ACK* → DONE` 每条记录追加写并
  fsync；`DECISION` 是原子提交点，一旦落盘绝不反转。崩溃导致的撕裂尾行在
  重放时被忽略（`wal.go`）。
- **投票阶段**：参与者超时未响应记为 NO（presumed abort），事务不会无限
  阻塞；重复投票、迟到投票、非成员投票均被幂等去重（`RecordVote`）。
- **决议分发**：恢复/完成阶段先 `Query` 参与者状态——已持最终状态则直接
  ACK（幂等送达），否则重发决议；超过 `MaxRetries` 仍不可达的参与者记录
  `AUDIT` 未决审计事件，事务保持 pending，后续 `Recover()` 继续推进，
  参与者恢复后事务闭环，不泄漏资源。
- **崩溃恢复**：`NewCoordinator` 重放 WAL 重建内存状态，`Recover()` 对所有
  未 `DONE` 的事务补做决议（无 DECISION 记录时 presumed abort）并完成分发。

## 运行

```sh
# Go 1.11 需显式开启 module 模式（更高版本直接 go test ./...）
GO111MODULE=on go test ./...        # 测试
GO111MODULE=on go run ./cmd/demo    # 崩溃-恢复演示
```

## 文件

- `coordinator.go` — 协调器状态机：投票、决议、分发、恢复、审计
- `wal.go` — JSON-lines 文件 WAL（fsync）与内存 WAL
- `participant.go` — 模拟参与者与进程内传输层（可模拟崩溃/超时）
- `coordinator_test.go` — 验收测试
- `cmd/demo/main.go` — 运行样例：发布 Commit 前崩溃 → 重启恢复 → 未决审计
