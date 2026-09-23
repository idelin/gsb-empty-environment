# 两阶段提交（2PC）与持久决策记录 — Go 实现

在**单进程离散事件模拟器**中实现经典两阶段提交。通信、故障、时间全部由模拟器
确定性地控制；协调者与参与者都把决策/投票写入持久 WAL，崩溃后重放日志恢复。

仅使用 Go 标准库；所有验证均通过 `go test` 完成。

- `sim/` — 离散事件模拟器（虚拟时钟、定时器、可控网络、持久存储、崩溃/恢复）
- `twopc/` — 协调者、参与者、WAL 记录、协议消息、集群装配与全部测试
- `cmd/twopc-demo/` — 两个脚本化场景的可运行示例
- `docs/sample-output.txt` — demo 实际输出
- `docs/test-output.txt` — `go test -v` 实际结果

## 运行

```sh
go run ./cmd/twopc-demo   # 两个场景的完整消息轨迹
go test ./...             # 全部测试
go test -race ./...       # 竞态检查（模拟器单线程事件循环，无数据竞争）
```

## 模拟器：把分布式系统变成确定性序列

代码不使用真实时钟、真实网络或 goroutine 驱动节点逻辑（仅事件循环单线程推进），
因此同样的输入必然产生同样的运行（见 `TestDeterministicReplay`）。

- **时间**：`sim.Scheduler` 维护虚拟纳秒时钟，事件按 `(时间, 序号)` 严格排序，
  节点只能读取 `ctx.Now()`，定时器通过 `ctx.After` 调度、`Timer.Stop` 取消。
- **通信**：`sim.Network` 对每次发送决定延迟（默认单向 5ms）、是否丢弃
  （`DropFunc`）、是否复制出重复副本（`Replicas`）。发往已崩溃节点的消息在投递时
  直接丢弃；重复副本共享同一发送序号。
- **故障**：`Fail(id)` 立即标记节点宕机（不执行任何 I/O，挂起定时器失效），
  `Recover(id)` 重新调用节点的 `Start`，由节点重放持久日志完成恢复；
  `FailAt/RecoverAt` 在指定虚拟时刻注入。
- **持久存储**：`sim.StableStorage` 模拟磁盘，追加是原子的，崩溃不丢，
  只有显式 `WipeStorage` 才清除。WAL 记录格式为
  `uvarint 长度 | JSON 负载 | CRC32`，可识别损坏/截断的尾部（`ErrTruncatedTail`）。

## 协议

### 协调者日志（每事务）

1. `BEGIN`：参与者集合 + 写集合在发送任何 `PREPARE` **之前**强制落盘；
2. `COMMIT`：收到**全部**赞成票后、发送任何 `COMMIT` **之前**强制落盘；
3. `ABORT`：收到 NO 票或投票超时时强制落盘。

### 参与者日志（每事务）

- `PREPARED`：检查所有锁空闲后，先把赞成票和锁信息落盘，再回复 `PREPARED`；
- `COMMITTED`：收到 `COMMIT` 后本地应用写、释放锁、落盘；
- `ABORTED`：NO 票/超时/收到 `ABORT` 后落盘并释放锁。

### 恢复决策表（协调者严格按日志，不得猜测）

| 重放结果 | 恢复动作 |
|---|---|
| 有 `COMMIT` 记录 | 决策=提交，向所有参与者重发 `COMMIT`，周期重传直到全部 ACK |
| 有 `ABORT` 记录 | 决策=中止，向所有参与者重发 `ABORT`，周期重传直到全部 ACK |
| 只有 `BEGIN`，无任何决策记录 | **强制写 `ABORT`（presumed abort）**，然后通知参与者 |

关键安全性质：**全部参与者 PREPARED 之后、协调者在写决策记录前崩溃，恢复时绝不允许
"猜提交"**。没有 `COMMIT` 记录就不能证明提交合法，因此只能中止
（见 `TestAllPreparedThenCoordinatorCrashAborts`）。在决策记录之后崩溃则相反，
`COMMIT` 已持久，恢复必须继续提交（见 `TestCrashAfterCommitRecordStillCommits`）。

参与者重启时重放日志：`PREPARED` 事务重建锁并立即向协调者发 `DECISION?` 查询；
协调者对未知事务按 presumed abort 回答 `ABORT`。

### 幂等与迟到消息

- 重复 `COMMIT`：已提交的参与者只重新 ACK，**绝不重复应用**；
  未投过赞成票却收到 `COMMIT` 属于违例，记录为 safety violation 而不是执行。
- 重复 `PREPARE`：`PREPARED` 状态重发赞成票；`ABORTED` 状态重发 NO 票。
- **`ABORT` 之后迟到的 `PREPARE` 不能重新占锁**：参与者已处于本地 ABORTED，
  锁早已释放且不会再被该事务获取，直接回 NO 票
  （见 `TestLatePrepareAfterAbortCannotRelock`）。
- 迟到投票在决策之后到达：协调者把已定决策单播给该迟到者。

## 阻塞情形（活性边界）

2PC 是**阻塞式**原子承诺协议。下列情形中资源会被不确定（PREPARED）的参与者
持续锁住，任何一方都不能单方面推进：

1. **协调者在决策窗口内崩溃且不恢复**：所有参与者都在 `PREPARED`，既不知道
   能否提交（别的节点可能投了 NO），也不敢擅自中止（协调者可能已写了 COMMIT）。
   它们只能持有锁等待；协调者恢复即解除（`TestPreparedParticipantsBlockWhileCoordinatorDown`
   先断言 t=40ms 全员仍 PREPARED 持锁，恢复后全部 ABORT 释放）。
2. **不确定参与者与协调者同时崩溃**：参与者重启后发 `DECISION?`，若协调者仍
   宕机则按固定间隔重试，直到协调者恢复。
3. **网络分区**：不确定一侧若无法联系协调者即阻塞；分区愈合后由决策重传或
   查询解除。
4. **参与者在 YES 之前崩溃**：协调者收不到赞成票，`VoteTimeout` 到期后中止，
   因此该情形不阻塞协调者；参与者也不持锁。

> 非阻塞原子承诺需要 3PC/多数派协调者等额外机制，超出 2PC 范围。本实现中协调者
> 存活时投票超时保证不会因缺票无限等待，决策重传 + 参与者恢复查询保证有限故障下
> 最终所有参与者获知同一结果。

## 安全性证据（不变量，任何执行都成立）

记协调者最终决策为 `D(T)`。

1. **原子性/一致决策**：参与者只有收到协调者的 `COMMIT` 才会提交，只有收到
   `ABORT`（或自己投 NO）才会中止；协调者的决策一旦持久不可更改（恢复表只可能
   读出同一条记录）。因此不可能出现一个参与者提交而另一个中止。
   *证明要点*：参与者 `COMMITTED` 的唯一前置是 `PREPARED` + 协调者 `COMMIT`；
   协调者发 `COMMIT` 的唯一前置是持久 `COMMIT` 记录。
2. **无记录则无提交**：协调者发 `COMMIT` 必先追加 `COMMIT`（WAL 先写规则）；
   没有该记录的恢复路径只会 abort。故"全 prepare 后崩溃"绝不可能自行猜提交。
3. **锁与投票一致**：`PREPARED` 回复只在锁全部获取且记录落盘后发出，因此一个
   YES 票在恢复后仍对应有效的锁；冲突键在阶段 1 直接 NO 且不部分占锁。
4. **幂等**：终态（`COMMITTED`/`ABORTED`）下重复决策只重发 ACK，不重复改 KV、
   不重新加锁；`ABORTED` 后迟到 `PREPARE` 回到 NO 票路径。
5. **WAL 完好性**：每条记录带长度前缀和 CRC，截断/损坏尾部被识别并拒绝执行
   （`TestWALDetectsTornAndCorruptTail`）；代码任何违反上述规则的情况都会被
   节点记入 `SafetyViolations()`，全部测试断言该列表为空。

## 活性证据（有限故障模型下）

假设：崩溃节点最终恢复、链路丢包/重复有界（或持续时间有限）、虚拟时钟推进。

- 协调者存活时：收不齐票由 `VoteTimeout` 强制 abort；收到任意 NO 立即 abort；
  收齐全部 YES 立即 commit。决策不被无限推迟。
- 决策已定时：协调者以 `RetryDelay` 周期重传，仅针对未 ACK 的参与者；
  存活参与者最终收到并 ACK（`TestLostMessagesRepairedByRetransmission` 丢弃首个
  COMMIT，验证重传修复）。
- 参与者在 `PREPARED` 崩溃：重启重放重建锁并周期 `DECISION?`；协调者按日志
  给出唯一答案（`TestParticipantCrashAfterPreparedRecoversAndCommits`）。
- 协调者崩溃：重启按恢复决策表给出唯一结果并重传；`BEGIN`-only 事务也会被强制
  abort，不会悬挂。

活性的唯一例外正是上面列出的阻塞情形：协调者（永久）不可达时，不确定参与者无法
获知决策。这是 2PC 在该故障模型下的固有限制，不是实现缺陷。

## 测试一览

| 测试 | 覆盖点 |
|---|---|
| `TestWALRoundTrip` / `TestWALDetectsTornAndCorruptTail` | WAL 编解码、CRC、截断尾部 |
| `TestHappyPathCommits` | 正常提交、KV 生效、锁释放 |
| `TestLockConflictAborts` | 阶段 1 锁冲突 → NO → 全局中止且不占锁 |
| `TestAllPreparedThenCoordinatorCrashAborts` | **全 PREPARED 后崩溃、无决策记录不得猜提交**，恢复 abort 放锁 |
| `TestCrashAfterCommitRecordStillCommits` | COMMIT 记录后崩溃，恢复继续提交 |
| `TestDuplicateCommitAndPrepareIdempotent` | 重复 PREPARE/COMMIT 幂等 |
| `TestLatePrepareAfterAbortCannotRelock` | abort 后迟到 prepare 不重新占锁 |
| `TestParticipantCrashAfterPreparedRecoversAndCommits` | 参与者持锁崩溃 → 恢复查询 → 提交 |
| `TestLostMessagesRepairedByRetransmission` | 丢包由决策重传修复 |
| `TestPreparedParticipantsBlockWhileCoordinatorDown` | 显式阻塞窗口 + 恢复解除 |
| `TestDeterministicReplay` | 相同输入两次运行消息轨迹逐字节一致 |
