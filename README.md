# DNS 标签外传检测包

`taskpkg` 供 Go 探针做流式 DNS 外传检测。检测模型为同一客户端向同一注册域发送十六进制标签，标签前 4 个十六进制字符为片号，其余字符为数据。

## 使用

```go
win := taskpkg.Window{Start: start, End: end} // 左闭右开
d := taskpkg.NewDetector(taskpkg.Config{K: 4, B: 32})
d.SetWindow(win)

for _, q := range queries {
    d.Observe(q)
}

alerts := d.AdvanceWatermark(win.End)
```

`Alert` 包含重组出的 `Data`/`DataHex`、参与重组的 `Fragments`、每个分片的原始 `QueryIDs`，以及单列的 `Conflicts`。同一片号和相同内容的重传只记一次不同片号；同一片号的不同内容进入 `Conflicts`，不覆盖最早时间戳的规范内容。

## 规则

- 窗口边界由调用方给定，按 `[Start, End)` 处理。
- 窗口内不同片号数不少于 `K`，且存在一段连续片号重组出不少于 `B` 字节时告警。
- 相同片号、相同内容的重传不增加不同片号数。
- 相同片号、不同内容单列为 `Conflict`；规范内容按最早时间戳确定，因此结果不依赖到达顺序。
- 非十六进制标签、数据不足整字节的标签不参与检测。
- `AdvanceWatermark(t)` 封口所有 `End <= t` 的窗口；封口后的迟到事件被丢弃。
- `Evaluate(events, win, cfg)` 提供同一窗口的全量重算，可用于与流式结果对照。

`go test ./...` 覆盖乱序、重传、冲突、合法长标签流、窗口边界与水位线封口。
