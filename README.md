# 有状态策略更新安全窗

网关更新连接跟踪规则时，既有连接与新连接必须遵守不同的明确策略。本仓库在
`example.org/netsec/uid1289/session` 提供一个可导入的 Go 数据面会话决策包。

## 核心语义

- **建立时固定策略代次**：`Engine.Connect` 生成的 `Tuple` 包含 `StartupGen`，
  会话在整个生命周期内按建连代次授权；后续 `Update` 只提升当前代次，不回溯
  修改既有会话。
- **两种更新模式**：
  - 仅新建生效：`engine.Update(policy)` 之后，只有新连接按新策略评估。
  - 同时撤销指定会话：`engine.Update(policy, session.WithRevocation(tuple...))`
    在同一更新中将指定活动会话置为 `StateRevoked`，未命中的元组通过
    `UpdateResult.Missing` 返回而不会使更新失败。
- **元组含方向与启动代**：`Tuple = FlowID + Direction + StartupGen`。同一传输
  五元组在旧会话结束后重新建连时得到全新元组，必须重新评估，不能继承旧授权。
- **迟到结束事件安全**：`End` 按完整元组精确匹配，携带旧 `StartupGen` 的结束
  事件只会命中旧会话（返回 `ErrSessionClosed`/`ErrSessionUnknown`），不会删除
  同一流上的新连接。

## 注入点

- `Clock`：生产环境使用 `SystemClock`，测试使用只进不退的 `ManualClock`。
- `Scheduler`：`SerialScheduler` 以 FIFO 手动派发事件，可严格控制
  “更新 → 建连 → 首包发送”的线性顺序；`Scheduled*` 方法把操作入队，
  `DispatchOne`/`DispatchAll` 决定派发时机。
- `Policy`：`RuleTable` 按“流 + 方向”精确匹配，未命中走默认决策，也可自行
  实现接口接入外部策略引擎。
- `Engine.VerifyOrder` 校验事件日志的序号与时间戳单调递增，以及建连代次与
  策略更新之间的线性关系。

## 快速上手

```go
engine := session.NewEngine(
    session.NewRuleTable(session.DecisionAllow),
    session.NewManualClock(time.Now()),
    session.NewSerialScheduler(),
)

tuple, err := engine.Connect(flow, session.DirectionEgress) // 固定当前代次
if err != nil { /* ErrConnectionDenied 或 ErrInvalidTuple */ }

if err := engine.SendFirstPacket(tuple); err != nil { /* 状态迁移 new -> established */ }

// 仅对新连接生效
engine.Update(session.NewRuleTable(session.DecisionDeny))

// 或在更新时撤销指定会话
engine.Update(newPolicy, session.WithRevocation(tuple))
```

## 测试

```sh
go test -race -cover ./...
```

测试覆盖：代次固定与祖父连接豁免、带撤销的更新、元组重用不继承授权、迟到结束
事件不删除新连接、注入时钟/调度器下的线性顺序，以及策略表、时钟与调度器组件。
