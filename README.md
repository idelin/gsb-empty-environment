# 配额权益与原子记账（单进程事件模拟器）

把一个**全局总配额 Q** 切分给若干**副本（Replica）**，每份是该副本可独立使用的“权益
（credit）”。副本可以在**离线**状态下扣减本地权益，可以把权益转移给其他副本；系统保证：

- 离线扣减永远**不超过全局量**（守恒，且本地不可透支）；
- 每次权益转移有一个**全局唯一 ID**，以该 ID 为键做**原子记账**；
- 网络重复投递、发送方超时重试、副本崩溃恢复都**不会凭空造出额度**；
- 发送方已扣、接收方未收到时，**总可用量只减不增**；
- 在公平调度下所有转移**最终完成**（活性）。

通信、故障、时间全部由一个**单进程离散事件模拟器**控制，运行完全确定性；只使用 Go
标准库，测试全部在 `go test` 内。

## 运行

```bash
go test -v -count=1 ./sim/     # 单元测试 + 确定性 Example
go run ./cmd/demo              # 可复现的端到端演示
```

> 若环境的 `GOCACHE` 目录不可写，可指定可写目录：`export GOCACHE=/tmp/gocache`。

## 目录结构

| 文件 | 作用 |
| --- | --- |
| `sim/simulator.go` | 虚拟时钟、事件堆、单线程事件循环、崩溃/重启 |
| `sim/message.go` | 消息类型与全局唯一 `TransferID{From, Seq}` |
| `sim/faults.go` | 网络模型：时延、丢包、重复（完美网络/随机故障/脚本故障） |
| `sim/disk.go` | 持久化 WAL（`append`+`fsync`）与持久去重表 |
| `sim/replica.go` | 副本状态机：`Spend` / `Transfer` / 收包 / 超时重传 / 崩溃恢复 |
| `sim/audit.go` | 从持久状态计算全局快照并校验安全不变式 |
| `sim/sim_test.go`、`sim/example_test.go` | 测试 |
| `cmd/demo/main.go` | 运行样例 |

## 协议：一条转移的原子记账

记副本状态为 `B`（可花余额）、`S`（累计离线扣减）、`P`（未完成的转出托管表）。
金额在 `open` 的同一笔 WAL 事务里**立即离开可花余额进入托管（escrow）**，之后任何一步
都不会再挪动同一份钱。

| 阶段 | 动作（先落盘 fsync，再产生外部效果） | 发送方 | 接收方 |
| --- | --- | --- | --- |
| 1 open | 发送方检查 `amount <= B`，写 `opOpen{id}` | `B -= a`，`P[id]=a` | — |
| 2 deliver | 接收方收到 `Transfer(id)`；**若 id 已存在于持久去重表则不入账**，只回 ack | 不变 | `B += a`（仅一次） |
| 3 ack | 发送方收到 `Ack(id)`；**若 id 已 ack 则忽略** | 删除 `P[id]` | 不变 |

重传：`open` 时与每次重启都为 `P` 中的转移设置虚拟定时器；超时后用**同一个 id**重发
`Transfer`。ack 丢失时接收方收到重复 `Transfer` 后**重发 ack**（幂等回应）。重复网络副本
与重试无法区分——模拟器的网络层**故意不去重**，去重只在应用层以 id 完成。

崩溃模型：`Crash` 清空全部进程内存（含定时器），`Disk`（WAL）保留；`Restart` 重放 WAL
重建 `B/S/P`，并为仍开放的转移重新挂上定时器。`persist()` 保证“先落盘后发包/返回成功”，
因此崩溃窗口里不会有“钱扣了却没有记录”的转移。

## 安全性证明（Safety）

事件循环单线程，任意时刻只有一个状态转移，因此“检查去重键 → 追加 WAL”对其他事件是
原子的；所有转移只有下表四种，且每笔金额 `a > 0`。

定义：`B = Σ Bᵢ`，`S = Σ Sᵢ`，`E = Σ Pᵢ`（托管总额），`D` = 已在接收方 `deliver`
入账、但发送方尚未 `ack` 的金额合计。

初始（只有 genesis）：`B = Q`，`S = E = D = 0`，故 `B + S + E - D = Q`。

| 转移 | ΔB | ΔS | ΔE | ΔD | 对 `B+S+E-D` 的净影响 |
| --- | --- | --- | --- | --- | --- |
| spend | -a | +a | 0 | 0 | 0 |
| open（转出） | -a | 0 | +a | 0 | 0 |
| deliver（接收） | +a | 0 | 0 | +a | 0 |
| ack | 0 | 0 | -a | -a | 0 |
| 重复 transfer | 0 | 0 | 0 | 0（id 已持久存在，拒绝入账，只回 ack） | 0 |
| 重复 ack | 0 | 0 | 0 | 0（id 已 ack，忽略） | 0 |
| 崩溃/重启 | 0 | 0 | 0 | 0（内存按 WAL 重放，磁盘不丢） | 0 |

归纳可得**任意时刻（含在途、含崩溃）**：

- **守恒**：`B + S + E − D = Q`，恒等。
- 网络排空后 `D = 0` 且 `E = 0`（见活性），于是 `B + S = Q`：离线扣减不超全局量。
- **非负**：spend/open 都有前置检查 `amount <= B`（可花余额不含托管），所有账户项
  非负——**不能透支本地权益**；`Audit()` 对每项非负做断言。
- **不造额度**：`deliver` 仅在 id 不在持久去重表时入账；入账与写键在同一 WAL 记录里，
  崩溃恢复后依然为真，因此重复消息/重试/重放合计只入账一次。
- **只减不增窗口**：阶段 1 之后到 deliver 之前 `B` 减少 `a`、`S/E` 增加但不进入任何
  可花余额，因此“发送方已扣、接收方未收到”时**总可用量 B 严格减少 a**；deliver 后恢复，
  ack 不再改变 B。重复副本被拒绝，B 不会被抬高。

`sim.Audit()` 在每个测试（含混沌测试的多个中间时刻）从持久日志重算上述全部量并校验
非负与守恒；`D` 由“接收方有 deliver 记录且发送方无对应 ack 记录”的转移金额求和得到。

## 活性证明（Liveness）

假设（公平调度，可在模拟器中强制满足）：故障不永久持续——每条消息在任意多次尝试后都有
一次被投递，且虚拟时间正常推进、副本最终重启。

对任意开放的转移 id：

1. open 已落盘。发送方运行时总有一个超时定时器；发送方崩溃后，重启会为该 id 重新挂
   定时器。因此 `Transfer(id)` 被无限次重发。
2. 以概率 1（测试中随机故障；脚本故障保证有一次成功投递）存在某次 `Transfer` 到达存活
   接收方，执行唯一一次 `deliver` 并回 ack；接收方若当时已崩溃，其重启后下一次重试同样
   会成功。
3. 接收方对每个（含重复的）`Transfer` 都回 ack；因此 ack 也被无限次发送，必有一次到达
   存活发送方，执行 `ack` 并删除 `P[id]`。之后所有重发/重复消息都成为幂等空操作。
4. 终止性：成功的 open/deliver/ack 数有界（open 数有限），定时器在 ack 后不再重挂；一旦
   所有 ack 完成且没有新工作，事件队列必然排空，`Run()` 结束。

混沌测试（`TestChaosConservation`，25 个随机种子）断言队列排空后每个副本
`PendingCount() == 0`，即上述活性在丢包率 0.35、重复率 0.2、多副本崩溃窗口下成立。

说明：这里的“离线扣减”是分区本地可用（AP 风格）；转移在 ack 前通过托管保护资金安全，
靠重传达到最终一致，而不是用全局阻塞来换取强一致。

## 测试结果

```
=== RUN   TestSplitAndOfflineSpend              配额切分、离线扣减、不可透支、不超全局量
=== RUN   TestTransferExactlyOnceUnderDuplication 网络重复下只入账一次
=== RUN   TestInFlightAvailabilityAndNoOverdraft  在途窗口总可用只减；托管金不可再花/再转
=== RUN   TestSenderCrashAndRestartCompletesTransfer 发送方崩溃重启后重传并完成
=== RUN   TestReceiverReplayAfterCrashCreditsOnce 接收方崩溃恢复后重试不二次入账
=== RUN   TestReceiverCrashKeepsDurableBalance    余额来自 WAL，崩溃不丢
=== RUN   TestChaosConservation                   25 种子：丢包/重复/崩溃，全程守恒且收敛
=== RUN   TestDeterminism                         同种子两次运行逐字段一致
=== RUN   Example
--- PASS: ...
ok  	quotacredit/sim
```

## 演示样例（`go run ./cmd/demo`，固定种子，可复现）

3 个副本切分 Q=200（100/70/30）。第一次转移副本丢失后由超时重传恢复，重传副本被网络
复制一份（应用层拒绝），一笔 ack 丢失，R3 在到账后崩溃又重启恢复，最后超额消费被拒绝：

```
t=3    R1 spent 10 (balance 90)
t=5    R1 -> R2 transfer 20 opened (id 1#1)
t=13   [audit] while first copy is lost: available=170 (R1 escrow 20 reduces the total)
t=30   R1 timeout: resending transfer 1#1 -> R2 (attempt 2)
t=32   R2 credited 20 from R1 (id 1#1, balance 90)
t=32   R2 DUP transfer 1#1 from R1: no new credit, re-acking
t=33   R1 acked transfer 1#1 by R2 (complete)
t=35   R2 spent 15 (balance 75)
t=40   R2 -> R3 transfer 10 opened (id 2#1)
t=41   R3 credited 10 from R2 (id 2#1, balance 40)
t=42   R3 DUP transfer 2#1 from R2: no new credit, re-acking
t=43   R2 acked transfer 2#1 by R3 (complete)
t=60   replica 3 CRASHED (disk survives)
t=75   replica 3 RESTARTED, state recovered from disk
------------------------------------------------------------
R1: balance=70  spent=10  inflight=0
R2: balance=65  spent=15  inflight=0
R3: balance=40  spent=0   inflight=0
quota=200  available=175  spent=25 (available+spent=200)
network: sent=7 delivered=6 dropped=3 duplicate-copies=2
app dedup: duplicate transfers rejected=2, duplicate acks rejected=0
SAFETY: invariants hold; liveness: every transfer completed; exactly-once verified
```

注意 `t=13` 时第一个报文已丢失：发送方扣了 20、接收方还没拿到，总可用从 190 降到 170
（只减不增）；最终网络排空后 `available + spent = 200 = Q`，两个重复报文都没有产生新
额度。
