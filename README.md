# backtest — 事件驱动回测引擎

Go 1.11+，无第三方依赖。

## 核心语义

- **双时间戳**：每个 `Event` 有 `EventTime`（发生时间）和 `ArrivalTime`（到达时间）。
  策略只能在事件到达后看到它。
- **派发顺序**：按 `(ArrivalTime, Seq)` 升序；`Seq` 即输入切片中的序号，
  同到达时间严格按输入顺序派发。
- **迟到更正**：`EventCorrection` 通过 `CorrectsID` 指向被更正事件。更正到达后
  追加为新版本，之后的 `Read` 看到新值；已写入的动作日志是 append-only，
  历史动作永不被改写。
- **未来数据隔离**：引擎只在派发时刻把事件写入 store，队列中未到达的事件
  在 store 中物理不存在，策略无法意外读到未来。

## 审计与重放

- `Context.Read` 是策略读取数据的唯一入口，每次读取记录 `ReadRecord`
  （事件 ID、所读版本的到达时间、读取时刻）。`AuditLog.ValidateReadSet`
  验证每条读记录满足 `RevArrivalTime <= ReadAt`。
- `Replay(events, recorded, strategy)` 用相同输入重跑并逐条比对动作序列；
  `VerifyDeterministic` 多次独立运行确认动作序列完全一致。

## 运行测试

```sh
go test ./...
```
