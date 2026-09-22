# 事务性 Outbox + 至少一次投递（单进程事件模拟器）

在**单个进程**里用纯 Go 标准库模拟一个确定性的分布式场景，用来验证
事务性 outbox 模式的三条核心性质：

1. **接收记录与本地业务修改同事务**（inbox + 余额 + outbox 原子提交）；
2. **消息经 outbox 发出**（只有提交后的 outbox 记录才会被 relay 发送）；
3. **崩溃窗口用故障点重放**（故障点可武装、按标签精确命中，同一种子两次运行完全一致）。

## 运行

```bash
go run .                 # 端到端时间线演示
go test ./...            # 全部测试
go test -race ./...      # 竞态检查
```

> 若 `GOCACHE` 默认目录不可写，可 `export GOCACHE=/tmp/gocache`。

## 目录结构

| 路径 | 职责 |
| --- | --- |
| `sim/` | 离散事件循环（逻辑时钟、定时器堆）、可丢包消息总线、节点崩溃/重启、带预算的故障点、轨迹 |
| `store/` | 进程内“数据库”：快照事务 + 写入集，`Commit` 原子合并 |
| `bank/` | 上游服务（同事务 inbox/余额/outbox + relay）、下游幂等消费者、超时重投的客户端、安全不变量 |
| `main.go` | 三个故障窗口的端到端演示 |

## 协议与存储

- 客户端发 `Command{CmdID}`，超时未收到 `Receipt` 就**重投同一命令**（至少一次）。
- 上游处理命令时，一个事务内写入：
  - `inbox:<cmdID>`（接收记录/幂等键，重投时直接重放响应，不再改业务）
  - `acct:<acct>`（本地业务修改：余额）
  - `outbox:ev-<cmdID>`（待发消息，状态 `pending`）
- relay 周期扫描 `pending` 行发布 `Event`；收到下游 `Ack` 后在新事务中标记 `sent`。
- 下游用 `dsinbox:<eventID>` 去重：处理事件与记录 inbox/effect 同事务，重复事件只重发 ack。
- 事件在网络上的延迟用确定性 RNG 抽取；`DropRule` 可按消息内容有界丢包；
  发往崩溃中节点的消息一律丢弃，由对端重试。

## 故障点（崩溃窗口）

故障点是代码里的 `n.Fault("name:id")` 调用。`sim.Arm(label, n)` 在标签上
放置 `n` 次预算；触发到已武装标签时以 `panic` 方式崩溃（**跳过该事件剩余代码**），
并在固定延迟（12 拍）后重启节点。标签支持精确匹配与前缀匹配，因此可以
“在 c2 的提交后窗口崩溃”而不影响其它命令。

| 标签 | 位置 | 后果 |
| --- | --- | --- |
| `svc.before_commit:<c>` | 三行写入之后、`Commit` 之前 | 事务整体不存在：无 inbox、无余额、无 outbox，**消息从未外发**；客户端重投后成功 |
| `svc.after_commit:<c>` | 提交之后、发送响应之前 | 业务已生效；响应丢失，重投命中 inbox 重放响应，**余额不重复增加** |
| `svc.after_publish:ev-<c>` | relay 发出 event 之后 | 未及标记 `sent` 即崩溃；下轮重发 event，下游去重，**效果恰好一次** |
| `svc.before_mark:ev-<c>` | 收到 ack、标记之前 | 与 ack 丢失等效：重发 + 去重 |
| `ds.before_commit:ev-<c>` | 下游提交效果之前 | 无效果；上游重发后重试成功 |
| `ds.after_commit:ev-<c>` | 下游效果提交后、ack 前 | 效果已在；重发的重复事件命中去重，只重发 ack |

## 安全性证明（不变量）

`bank.AssertSafety` 被注册为模拟器回调，在**每个事件处理之后**运行；
任何违反都会让测试立刻失败（见 `bank/invariants.go`）：

- **S1 同事务原子性**：每条 `inbox` 行必有同 `CmdID` 的 `outbox` 行；
  账户余额恒等于已提交 inbox 命令金额之和。因为三行在一个事务中，
  `store` 的提交是原子的，故障要么发生在提交前（三者皆无），
  要么发生在提交后（三者皆有），不存在中间态。
- **S2 未提交不外发**：重放全局轨迹，`event` 的 `deliver` 事件必须
  晚于其 outbox 的 `commit` 轨迹。relay 只能读到已提交的 outbox 行，
  未提交事务的写入在私有写入集中，任何节点都看不到。
- **S3 下游幂等（效果恰好一次）**：每个 `eventID` 至多一个 `effect` 行，
  且必有 `dsinbox` 行。重复投递要么在事务前被去重，要么下游已崩溃
  （效果事务未提交），不可能产生两个效果。
- **S4 状态一致**：被标记 `sent` 的 outbox 必有对应下游效果；
  效果集合只会引用已存在的 outbox。

以上由 6 个定向故障测试 + 40 组随机重放（6 个故障点在每条命令上最多
各崩溃一次，并叠加确定性有界丢包）持续校验。

## 活性证明（最终收敛）

在「故障次数有限、丢包有界（同一条消息之后的重试必通）」的调度公平性
前提下，对每条命令作阶段推进论证：

1. 客户端每 25 拍重投未应答命令；投递成功后上游要么事务提交、要么崩溃，
   崩溃会重启且重投继续，因此**命令最终提交**（S1 保证只生效一次）。
2. relay 每 10 拍重新发布所有 `pending` 行；event 终会投递成功并被下游
   处理（下游崩溃后重启、重复投递幂等）。
3. 下游效果提交后重发 ack 直到成功；ack 使 outbox 变 `sent`，不再重发。
4. 客户端收到响应后完成。每个阶段的重试定时器都是有限且持续重设的，
   阶段编号严格递增，不存在循环依赖，故有限时间内全部命令收敛。

模拟器用有限事件预算断言该收敛（`RunUntil` 检查：客户端全部完成、
所有 outbox `sent`、效果数 = outbox 数）；预算耗尽即测试失败。

## 演示输出（节选）

```
== 故障窗口重放时间线（逻辑时钟）==
t7     deliver  cmd client->svc id=c1
t7     crash    svc @svc.before_commit:c1
t19    reboot   svc @svc.before_commit:c1
t31    deliver  cmd client->svc id=c1
t31    commit   outbox 已提交: ev-c1
t38    deliver  receipt svc->client id=c1
...
t57    deliver  cmd client->svc id=c2
t57    commit   outbox 已提交: ev-c2
t57    crash    svc @svc.after_commit:c2
...
t78    deliver  cmd client->svc id=c2          # 重投：命中 inbox，不再加钱
...
t109   crash    svc @svc.after_publish:ev-c3   # 已发送未标记
...
t135   deliver  event svc->ds id=ev-c3         # 重发，下游去重
t139   deliver  ack ds->svc id=ev-c3
t139   commit   outbox 已提交: ev-c3

账户 A 余额: 60 (期望 60；重投未重复加钱)
上游 inbox/outbox 行数: 3 / 3
下游 inbox/effect 行数: 3 / 3 (重复投递被去重)
event 发送 5 次、投递 5 次 (至少一次, 含重发)
收敛: true @t=139
```

完整输出见运行 `go run .`。

## 测试结果

`go test -v -count=1 ./...`（本机实际输出）：

```
--- PASS: TestBaseline (0.00s)
--- PASS: TestCrashBeforeCommit (0.00s)              # 提交前崩溃：不外发、重投成功
--- PASS: TestCommitThenResponseLost (0.00s)         # 提交后响应丢失：重投不改业务
--- PASS: TestPublishedBeforeMark (0.00s)            # 发送后未标记：重发 + 下游去重
--- PASS: TestDownstreamAckLost (0.00s)              # 下游 ack 前崩溃
--- PASS: TestResponseDroppedByNetwork (0.00s)       # 纯网络丢首包
--- PASS: TestRandomReplaySafetyAndLiveness (0.11s)  # 40 组随机种子重放
--- PASS: TestDeterministicReplay (0.00s)            # 同种子轨迹逐字节一致
ok  	outboxsim/bank
```

随机重放内含 40 个子用例（`#00`–`#39`），每组在 6 个故障点、4 条命令上
武装崩溃预算并叠加有界随机丢包；安全不变量逐事件检查，最终全部收敛。
`go test -race ./...` 同样通过。
