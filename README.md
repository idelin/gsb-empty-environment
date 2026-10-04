# pixelagg

像素事件聚合库：接收坐标、极性和时间戳的事件流，按固定时间窗聚合，
以水位（watermark）加有界乱序（allowed lateness）关闭窗口。

## 语义

- 事件：`{ID, X, Y, Polarity, Timestamp}`，`ID` 唯一，重复事件被丢弃。
- 窗口：半开区间 `[start, start+windowSize)`，边界时间戳属于下一个窗口；
  负时间戳通过向下取整正确落入负窗口。
- 水位：已见事件时间戳的最大值。当 `watermark >= windowEnd + allowedLateness`
  时窗口关闭并输出结果。
- 聚合：每个窗口内按像素输出净计数（极性求和）与最早/最晚事件时间；
  计数抵消为 0 时时间摘要仍然保留。
- 迟到：窗口已关闭后到达的事件进入更正输出（`Corrections`）。
- `Flush()` 在输入结束时强制关闭所有剩余窗口。

## 示例

```go
a := pixelagg.New(10, 5) // 窗口 10，容忍乱序 5
a.Add(pixelagg.Event{ID: "e1", X: 1, Y: 2, Polarity: 1, Timestamp: 3})
a.Add(pixelagg.Event{ID: "e2", X: 1, Y: 2, Polarity: -1, Timestamp: 7})
a.Add(pixelagg.Event{ID: "wm", X: 0, Y: 0, Polarity: 1, Timestamp: 15}) // 关闭 [0,10)
results := a.Results()       // 像素 (1,2): NetCount=0, First=3, Last=7
late := a.Corrections()      // 超水位迟到事件
```

## 测试

```sh
go test ./...
```
