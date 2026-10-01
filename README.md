# protowire

零外部依赖（仅标准库）的 Protocol Buffers 二进制 wire-format 编解码器，
面向低延迟场景：热路径零堆分配、对象池化、并发安全、严格越界防护。

## 包结构

| 包 | 职责 |
|---|---|
| `wire` | 手写 wire-format 解析/编码：varint(7位压缩)、zigzag、fixed32/64、tag、length-delimited、嵌套子 Reader、字段跳过。所有读取均边界检查，截断/溢出/非法 wire type 返回哨兵错误，绝不 panic、绝不越界。 |
| `pool` | 分级（64B–64KB）字节缓冲池，原子 acquire/release 计数，双重释放检测，支持泄漏断言。 |
| `cache` | 32 分片定容缓存，clock-hand 驱逐；值存于池化缓冲；`Get` 拷贝至调用方 dst，热路径零分配。 |
| `msg` | 无反射示例 schema 编解码：嵌套消息、packed/unpacked repeated、字符串零拷贝；消息对象全部池化。 |
| `cmd/demo` | 端到端运行样例。 |

## 运行

```sh
go run ./cmd/demo          # 端到端样例：编码→缓存→零分配解码→截断防护→池统计
go test ./...              # 单元测试（边界、截断扫描、零分配、泄漏断言）
go test ./... -race        # 并发安全（race detector）
go test ./msg/ -bench=. -benchmem   # 基准：0 B/op, 0 allocs/op
```

## 关键设计

- **零拷贝**：字符串/字节/子消息均为输入缓冲的子切片视图，解析不复制数据。
- **零分配**：`Reader`/`Writer`/消息对象/缓存值全部池化复用；暖池后
  `BenchmarkDecodeOuter` 与 `BenchmarkEncodeOuter` 均为 `0 allocs/op`。
- **安全边界**：varint 超 10 字节或第 10 字节超 1 位载荷报 `ErrOverflow`；
  长度前缀超过剩余字节报 `ErrTruncated`（先校验后切片，无巨量分配风险）；
  非法字段号/wire type（含废弃的 group 类型 3/4）报 `ErrInvalidTag`/`ErrInvalidWireType`。
- **泄漏断言**：`pool.Stats()` 与 `msg.Stats()` 暴露 acquire/release 计数，
  测试在并发压测后断言 `InUse()==0`；双重释放返回 `ErrDoubleRelease`。
