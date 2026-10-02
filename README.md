# protowire

零外部依赖（仅 Go 标准库）的 Protocol Buffers 二进制 Wire-format 解析器、
反射模式校验引擎，以及自适应动态阈值伸缩与负载感知控制器。

## 结构

- `wire/` — 纯手工 Wire-format 编解码：Varint 7 位压缩、ZigZag 有符号转换、
  Fixed32/64、Length-delimited 嵌套消息与 Packed Repeated 数组；
  全程安全越界防护（`ErrTruncated` / `ErrOverflow` / `ErrInvalidWireType`）。
- `validate/` — 反射校验引擎：`pb:"num,kind"` 标签把字节流还原进结构体
  （支持嵌套 message、packed 切片），`validate:"required,min=,max="` 执行规则校验。
- `adaptive/` — 负载感知控制器：EWMA 平滑外部压力，AIMD（加性增/乘性减）
  动态伸缩阈值，迟滞带 + 降额冷却防止抖动与雪崩；并发安全，后台采样
  goroutine 可随 `Close`/ctx 取消干净退出，无泄漏。
- `cmd/demo/` — 运行样例：手工构造多层嵌套 + packed 数组的流，解析还原，
  演示截断防护与控制器随压力伸缩。

## 运行

```sh
go test -race -count=1 ./...   # 全部测试（含并发与 goroutine 泄漏断言）
go run ./cmd/demo              # 运行样例
```
