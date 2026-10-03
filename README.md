# pbcodec — 零依赖 Protobuf Wire-Format 编解码器（Go）

纯手工解析的 Protocol Buffers 二进制 Wire-format 实现，仅用标准库，
无任何外部依赖。面向紧凑持久化存储与内存映射（zero-copy）解析场景。

## 结构

- `wire/` — 底层 Wire-format 原语
  - Varint 7 位压缩编码（10 字节上限，第 10 字节 >1 判溢出）
  - ZigZag 有符号整数变换（`0→0, 1→-1, 2→1, …`）
  - Fixed32 / Fixed64 小端定长编解码
  - Length-delimited 记录与嵌套子消息
  - `Reader`：边界受检游标，每次读取先校验剩余长度，适合内存映射缓冲区
  - `Writer`：仅追加的编码器
- `codec/` — 反射模式校验引擎
  - 结构体标签 `` `pb:"<num>[,packed|rep]"` `` 驱动编解码
  - Packed / 非 Packed Repeated 数组（编码可强制，解码两种都接受并合并）
  - 多层嵌套消息，递归深度上限 `MaxDepth=100` 防栈耗尽
  - 向前兼容：内嵌 `codec.UnknownFields` 捕获并重发未知字段，跨版本无损中继
  - 向后兼容：流中未知字段安全跳过
  - 零拷贝还原：`string` / `[]byte` 字段直接别名输入缓冲区（`unsafe.String`）
  - 类型缓存使用 `sync.Map`，Marshal/Unmarshal 无共享可变状态，并发安全

## 字段类型映射

| Go 类型 | Wire 编码 |
|---|---|
| `bool, int32/64, uint32/64` | varint |
| `codec.SInt32 / SInt64` | zigzag varint |
| `codec.Fixed32 / SFixed32` | fixed32 |
| `codec.Fixed64 / SFixed64` | fixed64 |
| `string, []byte` | length-delimited（解码零拷贝） |
| `struct / *struct` | 嵌套 length-delimited 消息 |
| `[]标量` | repeated，默认 packed，`,rep` 强制逐元素 |
| `[]*struct` | repeated 嵌套消息 |

零值标量与空字符串按 proto3 语义省略，保证编码紧凑。

## 安全边界

- 字段截断 → `wire.ErrTruncated`（含 length 前缀超过剩余输入的"长度炸弹"，
  不会触发巨额分配）
- Varint 超 64 位 → `wire.ErrOverflow`
- 字段号 0 或 > 2²⁹-1 → `wire.ErrBadFieldNumber`
- 保留 wire type（3/4/6/7）→ `wire.ErrBadWireType`
- 嵌套超深 → `codec.ErrMaxDepth`
- 所有错误路径均不 panic、不越界；fuzz 135 万次执行无崩溃

## 运行

```sh
go test -race ./...     # 全部测试（含并发与泄漏断言）
go test -run Example ./codec/
go run ./cmd/pbdemo     # 端到端演示
go test -fuzz=FuzzUnmarshal -fuzztime=30s ./codec/
```

注意：解码为零拷贝，输入缓冲区在解码结果使用期间不得修改或释放。
