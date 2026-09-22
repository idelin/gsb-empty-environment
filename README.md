# twopc：带持久决策记录的两阶段提交事件模拟

仅用 Go 标准库，在单进程事件模拟器中实现 2PC：通信（投递/延迟/丢弃）、
故障（崩溃/重启）与时间（虚拟时钟）全部由 `Sim` 控制；协调者与参与者
各自持有 fsync 的 WAL，崩溃后内存状态全部丢失，仅靠日志恢复。

## 结构

- `wal.go` — 追加式持久日志（JSON Lines + fsync）：`CBegin`/`CDecision`（协调者）、`PPromise`/`PDone`（参与者）
- `coordinator.go` — 协调者状态机与恢复逻辑（`Recover`）
- `participant.go` — 参与者状态机：prepare 后持锁、决策幂等、迟到 prepare 拒绝
- `sim.go` — 事件模拟器：消息队列、虚拟时钟、`Crash`/`Restart` 注入
- `twopc_test.go` — 安全性与活性证据；`cmd/demo` — 运行样例

## 协议要点

- 参与者收到 `prepare`：资源空闲则先写 `PPromise`（fsync）并加锁，再投 YES；
  被其他事务占用则投 NO；对同一事务的重复 prepare 幂等重答。
- 协调者收齐 YES 后**先把 `CDecision: COMMIT` fsync 落盘，再发 commit**；
  任一 NO 立即落盘 ABORT 并广播。决策记录是提交消息存在的必要条件。
- 协调者重启恢复：日志有决策 → 原样重放（参与者幂等）；日志无决策 →
  presumptive abort，补写 ABORT，**绝不猜提交**。
- 参与者对未知事务的 abort 写中止墓碑（`PDone: aborted`），此后迟到的
  prepare 只能得到 NO，不会重新占锁；重复 commit/abort 幂等确认。

## 明确的阻塞情形（2PC 固有）

1. 参与者投 YES 后即持锁进入不确定窗口，不能单方面提交或中止。
2. 协调者收齐 YES 后、写决策前崩溃：全体参与者持锁阻塞，直到协调者恢复
   （恢复时按日志只能 ABORT）。
3. 协调者 `CDecision: COMMIT` 落盘后、消息发出前崩溃：参与者仍阻塞，
   恢复后按日志重放 COMMIT。
4. 参与者 promise 后崩溃：重启后从 WAL 恢复承诺并重新占锁，继续等待决策。
活性结论：无故障时协议终止；有崩溃时以阻塞换取安全，故障恢复后终止。

## 运行

```sh
go test -v ./...   # 安全性/活性证据
go run ./cmd/demo  # 五个场景的事件轨迹
```
