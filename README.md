# wirecodec

零外部依赖（仅标准库）的 Protocol Buffers 二进制 Wire-format 手工编解码器（Go）。

## 特性

- **Varint**：7 位 LEB128 压缩编码，64 位溢出检测（>10 字节或第 10 字节 >1 报 `ErrOverflow`）
- **ZigZag**：有符号整数变换（sint32/sint64），`MinInt64`/`MaxInt64` 边界正确
- **Fixed32/Fixed64**：小端定长编码，float/double 位级还原
- **Length-delimited**：字符串、字节、嵌套消息、Packed Repeated 数组
- **零拷贝还原**：`Reader.Bytes()` 返回输入缓冲区的容量受限子切片，不复制
- **向前兼容**：未知字段按 wire type 安全跳过；**向后兼容**：缺失字段保持零值，packed/unpacked 两种 repeated 编码均可解码
- **边界防溢出**：每次读取严格边界检查，恶意长度前缀/截断输入返回 `ErrTruncated`，绝不 panic
- **并发安全**：Reader 为纯游标，只读缓冲区可被多 goroutine 共享解码

## 结构

- `wire.go` — 标量编解码、`Reader`（边界检查 + 零拷贝）、`Writer`（紧凑编码）
- `messages.go` — 手写三层嵌套消息 `Person → Address → Geo`，含 packed repeated、zigzag、fixed32/64
- `wire_test.go` / `messages_test.go` — 26 个测试：往返、溢出、截断扫描、未知字段跳过、零拷贝断言、并发、泄漏断言
- `example/main.go` — 运行样例

## 运行

```sh
go test -race -cover ./...
go run ./example
```

测试结果：26/26 通过，race 检测干净，覆盖率 85.1%。
