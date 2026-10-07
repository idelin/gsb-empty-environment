# backtest：防未来数据的 Go 回测引擎

## 核心语义

- **双时间轴**：每条事件有发生时间 `OccurAt` 与到达时间 `ArriveAt`。
  策略在决策时刻 `t` 只能看到 `ArriveAt <= t` 的事件。
- **处理顺序**：事件按 `(ArriveAt, Seq)` 排序处理，同到达时间按输入序号 `Seq` 升序。
- **迟到更正**：同 `ID`、更高 `Version` 的事件到达后覆盖旧值，
  只影响其后的决策，已产生的历史动作不会被改写；
  晚到的低版本事件不会回滚已生效的高版本。
- **审计读集合**：策略对数据视图的每次读取都记录为 `Read{ID, Version, ArriveAt, ReadAt}`，
  运行结束后 `Audit.Verify()` 校验不存在 `ArriveAt > ReadAt` 的读取（即未读未来数据）。
- **重放核对**：`Replay` 用相同输入运行两次，逐条比对动作序列，验证确定性。

## 用法

```go
type myStrategy struct{}

func (s *myStrategy) OnEvent(e backtest.Event, view *backtest.View) []backtest.Action {
    px, ok := view.Get("price") // 只能读到已到达的最新版本，读取会被审计
    if !ok {
        return nil
    }
    if px.Value > 100 {
        return []backtest.Action{{Kind: "sell", Value: px.Value}}
    }
    return nil
}

res, err := backtest.Replay(backtest.NewEngine(&myStrategy{}), events)
// res.Actions：带决策时间与序号的动作序列；res.Audit：审计读集合（已验证）
```

## 测试

```sh
GOCACHE=/private/tmp/gocache go test ./...
```

覆盖：同到达按输入序号、无未来数据、迟到更正只影响后续、
乱序版本不回滚、重放确定性、到达早于发生被拒绝。
