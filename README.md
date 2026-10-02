# wirebuf

零外部依赖的 Protocol Buffers 二进制 Wire-format 解析器（Go，仅标准库，兼容 Go 1.11+），
面向高频调用路径做了 GC 停顿消除：稳态解码 **0 堆分配**。

## 特性

- **纯手工 Wire-format 解析**：Varint（7 位压缩）、ZigZag 有符号整数、Fixed32/64、
  Length-delimited 嵌套消息、Packed Repeated 数组
- **安全越界防护**：任何截断输入（字段值、长度前缀、嵌套消息、packed 载荷）都返回
  `ErrTruncated`，绝不越界读；varint 超 64 位返回 `ErrOverflow`；保留 wire type 返回
  `ErrInvalidWireType`
- **零拷贝**：Length-delimited 字段返回源缓冲的子切片
- **对象池化**：`Reader` / `Message`（含嵌套递归释放）/ `U64s` 向量全部池化复用
- **分档缓冲池**：`BufferPool` 按 2 的幂尺寸分档（16 B – 1 MiB）回收字节切片
- **零分配缓存**：`Cache` 定容直接映射，Put/Take/Peek 稳态无堆分配，并发安全
- **泄漏断言**：`Stats` 原子计数 + `Balanced()`，测试断言严格无泄漏

## 运行样例

```sh
go run ./cmd/demo
```

手工编码一个三层嵌套、含 packed 数组的消息，解码并打印字段，演示缓存命中与
截断防护，最后验证池无泄漏。

## 测试

```sh
go test ./...        # 单元 / 边界 / 零分配 / 无泄漏
go test -race ./...  # 并发安全（16 goroutine x 500 轮混合池化解析与缓存）
go test -run '^$' -bench . -benchmem
```

基准（darwin/amd64）：池化解码 `0 allocs/op`，约为非池化路径的 2 倍吞吐。

## API 速览

```go
m := wirebuf.AcquireMessage()          // 池化消息
defer m.Release()                      // 递归归还（含嵌套消息与 packed 切片）
if err := wirebuf.Decode(buf, m); err != nil { ... }

id := m.Field(1).Varint                // varint 字段
delta := m.Field(2).ZigZag()           // zigzag 有符号字段
inner, _ := m.Field(5).DecodeNested()  // 嵌套消息（池化）
scores, _ := m.Field(6).DecodePacked() // packed repeated（池化）
```
