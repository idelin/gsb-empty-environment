# 弱有序事件流的拓扑重排稳定化窗口

面向乱序监控事件的稳定性延迟窗口：设定最大逻辑时间乱序窗口 `K`，
收集乱序事件流，在逻辑时钟推进越过安全线后，输出严格按因果全序
`(Timestamp, Seq)` 排列的标准化事件序列。

## 原理

- 算子内部维护一个按 `(Timestamp, Seq)` 字典序排列的优先级队列（最小堆）。
- 跟踪已观测的最大逻辑时间戳 `maxSeen`，安全水位线 `watermark = maxSeen - K`。
- 仅当事件的 `Timestamp <= watermark` 时才从堆中释放，保证输出流严格具备
  全局时态单调性。
- 相同时间戳的事件按到达序号 `Seq` 决胜，构成因果全序。
- `Timestamp < watermark` 的迟到事件被丢弃并计数（`Late()`），绝不破坏单调性。
- 输入结束后调用 `Flush()` 排空窗口内剩余事件。

## 使用

```go
s := stabilizer.New(3) // 最大乱序窗口 K=3
for _, ev := range stream {
    for _, ordered := range s.Push(ev.Timestamp, ev.Payload) {
        // 消费严格有序的输出事件
    }
}
for _, ordered := range s.Flush() {
    // 流结束，排空剩余事件
}
```

## 运行

```sh
go test ./stabilizer/ -v   # 测试
go run ./cmd/demo          # 运行样例
```
