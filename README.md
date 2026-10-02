# protokit

零外部依赖（仅标准库）的 Go 组件库：

- **`wire`** — 纯手工 Protocol Buffers 二进制 wire-format 解析器/编码器
- **`sparse`** — 海量稀疏数据的内存自适应紧凑集合容器

## wire：Protobuf Wire-Format 解析器

- Varint：7 位/字节压缩编码，10 字节上限，溢出检测（`ErrOverflow`）
- ZigZag：`sint32/sint64` 有符号整数转换
- Fixed32/Fixed64：小端定长解码，含 float32/float64 重解释
- Length-delimited：嵌套消息、字符串、Packed Repeated 数组
- 安全越界防护：所有读取均边界检查，截断输入返回 `ErrTruncated`，绝不 panic
- 游标式 API：`Reader.Next()` 逐字段迭代；`Skip()` 安全跳过未知字段

```go
r := wire.NewReader(data)
for {
    f, ok, err := r.Next()
    if err != nil { /* ErrTruncated / ErrOverflow / ... */ }
    if !ok { break }
    // f.Number, f.Type, f.Varint, f.Data, f.Fixed32, f.Fixed64, f.ZigZag()
}
```

## sparse：内存紧凑优化器

根据数据分布自动在三种后端间转换：

| 后端    | 适用分布 | 空间复杂度 |
|---------|----------|-----------|
| `array` | 极稀疏   | 4 B/值    |
| `rle`   | 连续聚簇 | 8 B/游程  |
| `bitmap`| 高密度   | 1 bit/槽位|

- 序列化采用 delta + varint 压缩，密集数据约 1 B/值
- `sync.RWMutex` 保护，读写并发安全（`-race` 验证）
- `Clone()` 深拷贝零共享；读路径零分配；无后台 goroutine 泄漏

## 运行

```sh
go test -race ./...   # 全部测试（含竞态检测）
go run ./cmd/demo     # 运行样例
```
