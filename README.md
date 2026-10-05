# delimparse

供导入器调用的分隔文本（CSV 风格）解析库。输入按块到达，解析器可暂停；
引号内换行与转义序列可以跨块。字段保留其在输入流中的原始字节区间
（绝对偏移），调用方可据此回切原始数据。

## 特性

- **可暂停解析**：`Feed(chunk)` 增量喂入任意切块，`Close()` 收尾；
  事件（记录 / 错误）随解析进度逐步返回。
- **原始字节区间**：每个 `Field` 记录 `[Start, End)` 绝对偏移（含引号），
  同时提供解码后的 `Value`。
- **非法引号按物理行隔离**：裸引号或闭引号后的垃圾字符触发错误，
  错误范围恰好覆盖一个物理行；坏行被丢弃，不会并入下一条记录。
- **单字段内存上限**：`MaxFieldBytes` 限制单字段解码后的内存；
  超限字段标记 `Truncated` 并丢弃 `Value`，但解析器继续扫描以定位记录终点。
- **转义**：默认双引号自转义（`""`）；设置 `Escape` 后按转义字符处理，
  转义序列可跨块。

## 用法

```go
p := delimparse.NewParser(delimparse.Options{MaxFieldBytes: 1 << 20})
for chunk := range chunks {
    for _, ev := range p.Feed(chunk) {
        switch {
        case ev.Record != nil:
            // ev.Record.Fields[i].Start/End 为原始字节区间
        case ev.Err != nil:
            // ev.Err.Start/End 为出错的物理行范围
        }
    }
}
for _, ev := range p.Close() { /* 处理尾部事件 */ }
```

一次性解析可用 `delimparse.Parse(data, opt)`。

## 测试

`parser_test.go` 对每组输入枚举所有 2 块与 3 块切分位置（以及逐字节喂入），
核对记录、原始区间、解码值、截断标记与错误范围均与一次读取一致：

```sh
go test ./...
```
