# delim

供导入器调用的可暂停分隔文本解析库。输入按块到达（`Feed`），引号内换行与
转义引号可跨块；每条记录解析完成即返回，解析器状态在块间保持。

## 用法

```go
p := delim.New(delim.Config{MaxFieldSize: 1 << 20})
for chunk := range chunks {
    for _, rec := range p.Feed(chunk) { /* 消费记录 */ }
}
for _, rec := range p.Close() { /* 末尾无换行的最后一条 */ }
```

## 语义

- **原始字节区间**：`Field.Start/End` 与 `Record.Start/End` 是输入流中的绝对
  字节偏移（字段区间含包围引号与转义序列），`Field.Value` 为解码后的值。
- **非法引号**：未加引号字段中的裸引号、或闭引号后的垃圾字符，触发
  `ErrIllegalQuote`（错误区间指向该引号字节）。此后按物理行隔离：坏行剩余
  部分被丢弃，记录在该行末尾结束，绝不把坏行吞进下一条记录。
- **单字段内存上限**：`Config.MaxFieldSize` 限制单字段解码后大小。超限时报
  `ErrFieldTooLarge`、该字段 `Value` 为 nil 并停止缓冲（内存有界），解析器
  继续按引号状态机扫描直至定位记录终点，再从下一条记录恢复。
- **未闭合引号**：EOF 时仍处于引号内，报 `ErrUnterminatedQuote`。
- 空行跳过；`\r\n` 视为行尾；`""` 为转义引号。

## 一致性

测试对每个用例枚举全部二切/三切分块位置，并对随机输入做随机分块，
核对记录、字段区间、错误区间与一次性读取完全一致（`go test ./...`）。
