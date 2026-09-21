# streamcol

流式有符号 64 位整数列编码格式。纯 Go 标准库实现，不依赖任何压缩库。

## 块格式

流 = 块序列。每块自描述、可独立跳过：

```
magic      2 B    0xC1 0x0A          —— 同步/再同步标记
verType    1 B    version<<4 | enc   —— enc: 0=raw 1=delta 2=delta-of-delta
flags      1 B    bit0: 独立基线
baseline   zigzag varint             —— 独立块为绝对锚点；依赖块为相对上一块末值的差
count      uvarint                   —— 元素数
payloadLen uvarint                   —— 载荷字节数
payload    payloadLen B
crc32      4 B    LE CRC-32C(verType..payload)
```

- **raw**：每元素绝对 ZigZag varint。
- **delta**：`v[i] = v[i-1] + d[i]`，`v[-1] = baseline`。
- **delta-of-delta**：`d[i] = d[i-1] + dd[i]`，`d[0]` 的前驱为 0。
- 依赖块的基线 = 上一块末值 + 存储差值（带溢出检查）；独立块直接存绝对锚点，
  是腐坏后恢复解码的锚点。

## 解码器的严格性

- varint 最长 10 字节；第 10 字节必须是 `0x01`（否则 64 位溢出），
  非最短编码（如 `0x80 0x00`）一律拒绝。
- delta / delta-of-delta 累加全程溢出检查，`MaxInt64`/`MinInt64` 不会悄悄回绕；
  编码端遇到不可表示的差分自动回退 raw（强制 delta 时报错）。
- 基线、元素数、字节数被 CRC-32C 覆盖，任何篡改都会被拒绝。
- 所有长度额度（单块元素数、载荷字节数、总元素数）在**分配之前**检查；
  `count > payloadLen` 直接判坏。
- 解析严格区分 `ErrNeedMoreData`（现有字节是合法前缀，等更多输入）与
  `*BlockError`（已有字节确定损坏，跳过并继续）。

## 流式与恢复

- `Feed` 接受任意分块（逐字节亦可），`NextBlock` 逐块吐出数据。
- 块损坏 → 报 `BlockError` 并精确跳过；依赖其基线的后续块报 `ErrBrokenChain`，
  直到下一个独立块恢复链条。头部无法解析时按 magic 再同步。
- `Checkpoint`/`ResumeDecoder` 支持断点续解；`context.Context` 支持取消。
- 解码同时构建随机定位索引（块偏移、首元素序号、绝对基线），
  `DecodeAt(data, idx, k)` 与顺序解码结果一致；索引条目自含绝对基线，
  依赖块也无需前驱即可随机读。

## 用法

```go
var buf bytes.Buffer
e := streamcol.NewEncoder(&buf, &streamcol.EncoderConfig{BlockSize: 256, Encoding: streamcol.EncAuto})
for _, v := range vals { e.Add(v) }
e.Close()

vals, idx, errs := streamcol.DecodeAll(ctx, buf.Bytes(), nil) // 顺序
v, _ := streamcol.DecodeAt(buf.Bytes(), idx, 1234)           // 随机
```

运行示例：`go run ./cmd/example`；测试：`go test ./...`。
