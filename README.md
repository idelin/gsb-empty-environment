# pixelagg

像素事件聚合库：接收坐标、极性与时间戳的事件流，按固定时间窗聚合，
支持有界乱序（水位线机制），迟到数据进入更正输出，重复事件按唯一 ID 去重。

## 语义

- 窗口为左闭右开区间 `[start, start+windowSize)`，事件 `t=end` 属于下一个窗口。
- 水位线 `watermark = max(已见事件时间) - allowedLateness`；
  当水位达到窗口右端时窗口关闭并输出。
- 事件时间早于其所属窗口关闭水位时，进入 `Corrections()` 更正输出，不计入窗口。
- 每个像素输出净计数（极性求和）与最早/最晚事件时间；
  计数抵消为 0 时，时间摘要仍然保留。
- 相同 `ID` 的事件只计入第一次；负时间与非 ±1 极性返回错误。

## 用法

```go
agg, _ := pixelagg.New(10, 2) // 窗口长度 10，允许乱序 2
agg.Add(pixelagg.Event{ID: "e1", X: 3, Y: 5, Polarity: 1, Time: 7})
// ...
agg.Close()                    // 流结束时冲刷剩余所有未关闭窗口
windows := agg.Windows()       // 已关闭窗口，按起始时间排序
late := agg.Corrections()      // 超水位迟到事件
```

## 测试

```sh
GOCACHE=/private/tmp/gocache GO111MODULE=on go test ./...
```
