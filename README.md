# wiresnap

零外部依赖（仅 Go 标准库）的三大组件：

| 包 | 功能 |
|---|---|
| `wire` | 纯手工 Protocol Buffers 二进制 wire-format 解析器/编码器 |
| `schema` | 基于反射的结构体 schema 校验引擎 + Marshal/Unmarshal |
| `snapshot` | 低停顿增量快照与 Checkpoint 归档 |

## wire — 二进制紧凑变长编解码器

- **Varint**：7 位/字节的 base-128 压缩编码，最多 10 字节，第 10 字节只允许 1 个有效位，超出报 `ErrOverflow`
- **ZigZag**：有符号整数映射（`0→0, -1→1, 1→2, ...`），`AppendZigZag` / `ReadZigZag`
- **Fixed32/64**：小端定长读写
- **Length-delimited**：长度前缀 + 子切片（零拷贝），支持嵌套消息
- **安全越界防护**：所有读取均做边界检查，截断输入返回 `ErrTruncated`，绝不 panic

## schema — 反射模式校验引擎

```go
type Vec3 struct {
    X float32 `protobuf:"fixed32,1"`
    Y float32 `protobuf:"fixed32,2"`
    Z float32 `protobuf:"fixed32,3"`
}
type Player struct {
    ID      uint64   `protobuf:"varint,1"`
    Name    string   `protobuf:"bytes,2"`
    Balance int64    `protobuf:"zigzag,3"`
    Pos     Vec3     `protobuf:"message,4"`   // 嵌套消息
    Scores  []int32  `protobuf:"zigzag,6"`   // Packed repeated
    Tags    []uint32 `protobuf:"varint,7"`
}
```

- `schema.Validate(v)`：校验 tag 可解析、字段号唯一、Go 类型与 kind 兼容（含嵌套递归校验）
- `schema.Marshal` / `schema.Unmarshal`：proto3 语义（零值字段省略），未知字段自动跳过，数值切片按 packed 编码，同时兼容解码非 packed 标量重复字段

## snapshot — 增量快照与 Checkpoint 归档

- `Store`：版本化并发安全 KV 状态，脏键集合（dirty set）跟踪增量（含删除墓碑）
- `Snapshotter.Full()` / `Incremental()`：锁内仅复制条目，序列化在锁外完成 —— 低停顿
- `Archive`：快照文件落盘（临时文件 + rename 原子提交），`Restore` 按序回放（full 重置 + inc 叠加）
- 快照文件本身用 `schema` 引擎编码，全链路零外部依赖

## 运行

```sh
go run ./cmd/demo     # 运行样例
go test -race ./...   # 全部测试（含竞态检测）
```
