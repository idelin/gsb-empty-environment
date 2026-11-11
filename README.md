# tsblock

采集代理的内存内时间序列块压缩器（Gorilla 风格），仅依赖 Go 标准库。

## 编码方案

- **时间戳（二阶差分变长编码）**：首个时间戳原样存 8 字节；首个时间增量存 64 位；
  之后存增量的差分（delta-of-delta），按取值范围选择 1/2/3/4 位前缀加
  7/9/12/64 位二补码载荷。
- **数值（XOR + 前导/尾随零窗口复用）**：相邻 `float64` 按位异或；XOR 为 0 只写 1 位；
  有效位完全落进上一窗口时复用前导/尾随零区间；否则写新的 5 位前导零 + 6 位有效长度。
  全程按原始 64 位模式处理，因此 **NaN 载荷、负零、无穷、次正规数都逐位保留**。
- **全字 XOR 转义**：当 XOR 既无前导零也无尾随零（需要 64 个有效位，超过 6 位长度字段）时，
  使用正常编码不可达的描述符 `(lead=0, len=0)` 作为转义码，后接完整 64 位 XOR。
- **按大小封口**：待写帧达到 `maxSize`（默认 `DefaultMaxBlockSize = 4096` 字节）后，
  下一次 `Append` 自动封口并开启新块；每个帧是自描述的，可独立解码。
- **拒绝乱序**：时间戳必须严格递增，重复或回退返回 `ErrOutOfOrder`。

## 帧布局

```
magic "TSB1" | numSamples uvarint | firstT 8B | [firstDelta 8B] | firstV 8B | bitLen uvarint | body
```

`body` 按 MSB 优先打包，`bitLen` 记录有效位数，末字节的填充位不参与解码。

## 用法

```go
enc := tsblock.NewEncoder(tsblock.DefaultMaxBlockSize)
err := enc.Append(tsblock.Sample{T: ts, V: v}) // 乱序返回 tsblock.ErrOutOfOrder
frames, err := enc.Seal()                       // [][]byte，每个元素是一个独立块

dec, err := tsblock.NewDecoder(frames[i])
ok := dec.Seek(targetT)                         // 块内按时间定位到首个 T >= targetT
for dec.Next() {
    _ = dec.Time()
    _ = dec.Value()                             // float64，逐位还原
}
```

`Seek` 只需重扫当前帧的时间戳流并同步 XOR 窗口状态，无需解码更早的块，即可从块中间定位。

## 测试与压缩率

- `go test ./...`：随机序列、真实形态序列（漂移/平台/跳变/周期，含 NaN 与负零）、
  随机位模式、极端时间戳、全字 XOR 转义均做**逐位相等**往返校验；另有乱序拒绝、
  按大小封口、损坏帧拒绝、块内 Seek 与 Seek 后继续扫描等测试。
- `go run ./cmd/tsblock-report`：对四类形态打印压缩率，并在报告前先校验往返逐位一致。

参考结果（20000 样本，原始 16 字节/样本）：

| 形态 | 压缩后占比 | 缩小倍数 |
| --- | --- | --- |
| 恒定时间戳与数值 | 1.6% | ~63x |
| 真实 CPU 形态 | ~49% | ~2.0x |
| 随机浮点 | ~59% | ~1.7x |
| 随机位模式（最坏情况） | ~63% | ~1.6x |
