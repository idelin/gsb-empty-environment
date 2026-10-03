# pbinspect — Protobuf Wire 编解码器 + 形式化约束巡检引擎（零依赖，纯标准库）

## 结构

- `wire/` — 手工实现的 Protocol Buffers 二进制 wire-format 编解码
  - `wire.go`：Varint（7 位压缩）、ZigZag 有符号转换、Fixed32/64、tag/length 原语；错误链 `Is/Unwrap`
  - `parser.go`：字段级消息解析，全部读取越界防护，截断报 `ErrTruncated`
  - `decoder.go`：反射解码器，struct tag `pb:"<num>,<kind>[,packed]"`，支持嵌套消息（指针/值）、Packed Repeated、未知字段跳过、singular last-wins
- `inspector/` — 离线完整性巡检与不变式断言引擎
  - 悬挂引用检测、孤立环路检测（Tarjan SCC + 根可达性）、数据倾斜检测（均值倍数阈值）
  - 用户注册不变式，在深拷贝快照上无锁求值；`sync.RWMutex` 保证并发安全，不派生 goroutine
- `cmd/demo/` — 运行样例：手工编码三层嵌套 + packed 数组的流，解码还原，截断防护演示，巡检报告

## 运行

```sh
go test -race -count=1 ./...
go run ./cmd/demo
```
