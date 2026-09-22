# deltacodec

按通道（channel）保存上一个值、对差值做 ZigZag+varint 编码的流式编解码器。
纯 Go 标准库实现，无第三方依赖。

## 特性

- **每通道独立基线**：`Encoder` 为每个 channel 保存 `last`，后续值编码为有符号差值。
- **全量锚点帧**：channel 首次发送、重置后、差值超出 `int64` 范围，以及每隔
  `SetAnchorPeriod(p)` 个差分后，自动发送绝对锚点；也可 `EncodeAnchor` 强制发送。
- **重置帧**：`Reset(ch)` 丢弃该通道基线，解码端在新锚点前输出“不可确定”。
- **缺测帧**：`EncodeMissing(ch)` 显式标记一次缺测，保留基线，后续差分仍可解。
- **绝不猜测**：没有基线（未锚定/已重置）或差分帧丢失时，解码结果为
  `StatusUnknown`，直到该通道的下一个锚点；通道之间状态完全隔离。
- **任意分块输入**：`Decoder.Feed` 接受任意大小的字节块，内部缓存半帧；
  `ErrNeedMoreData`（数据不够，继续喂）与 `ErrCorrupt`（坏数据，流终结）严格区分。
- **长度额度**：解析出 payload 长度后、任何按该长度的分配之前检查额度
  （默认 64 字节，`SetMaxPayload` 可调），超限直接判坏数据。
- **正确性**：有符号差值/求和溢出判定、负差值、`MinInt64/MaxInt64` 极值均有测试。

## 帧格式（自定界）

```
tag     1 字节   0x01 anchor | 0x02 delta | 0x03 missing | 0x04 reset
channel uvarint  uint32 通道号
seq     uvarint  每通道单调帧序号；锚点重新从 0 开始，用于发现“整帧丢失”
plen    uvarint  payload 长度
payload plen 字节  anchor/delta 为 ZigZag 编码的 int64 varint，其余为空
crc32   4 字节   对 tag..payload 的 IEEE CRC-32（小端）
```

为什么需要 `seq`：CRC 只能发现“到了但坏了”的帧；网络上“根本没到”的帧需要
每通道序号才能察觉。检测到序号空洞后，该通道的每个差分都标记为不可确定
（`nextSeq` 锁定期望序号），直到锚点重新建立基线。

## 解码状态

| 状态            | 含义                                                 |
|-----------------|------------------------------------------------------|
| `StatusOK`      | `Value` 为精确重建的测量值                           |
| `StatusMissing` | 发送方显式标记该次缺测                               |
| `StatusUnknown` | 无法从已收数据推导（无基线 / 曾丢差分），不得当作数值 |

## 快速使用

```go
var buf bytes.Buffer
enc := deltacodec.NewEncoder(&buf)
enc.SetAnchorPeriod(100)
enc.Encode(1, 100)        // anchor
enc.Encode(1, 103)        // delta +3
enc.EncodeMissing(1)      // 缺测，基线保留
enc.Reset(1)              // 丢弃基线

dec := deltacodec.NewDecoder()
dec.Feed(buf.Bytes())     // 可分任意次、任意大小喂入
for {
    sample, err := dec.Decode()
    if err == deltacodec.ErrNeedMoreData { break }
    if err != nil { panic(err) } // ErrCorrupt：坏数据，流终结
    // sample.Status / sample.Value
}
```

也可用 `NewStreamDecoder(io.Reader)` 直接从流读取（流结束返回 `io.EOF`，
半截帧返回 `io.ErrUnexpectedEOF`）。

## 运行样例

```
go run ./cmd/demo
```

输出（节选）：

```
== normal decode (chunk size 3) ==
  ch1  value=100
  ch2  value=-50
  ch1  value=103
  ch1  value=99
  ch1  value=120        # 第 3 个差分为定期全量锚点
  ch2  value=-55        # 负差值
  ch2  value=-55        # 零差值
  ch1  MISSING          # 显式缺测
  ch1  value=125        # 基线保留，差分仍可解
  ch2  <unknown: undeterminable, no baseline>   # reset 后

== after losing one channel-1 delta (note channel 2) ==
  ch1  value=100
  ch2  value=-50
  ch1  <unknown: undeterminable, no baseline>
  ch1  <unknown: undeterminable, no baseline>
  ch2  value=-55        # 通道 2 完全不受影响
  ch2  value=-55
  ch1  <unknown: undeterminable, no baseline>
  ch1  value=125        # 定期锚点到达，通道 1 恢复

== corrupted byte on the wire ==
  ch1  value=100
  ch2  value=-50
  ch1  value=103
  ch1  value=99
decoder stopped: deltacodec: corrupt stream (no guessed output)
```

## 测试

```
go test ./...
go test -race -v -cover ./...
```

测试结果：16 个测试全部通过，`-race` 干净，语句覆盖率 89.9%。
覆盖内容：往返编解码、负差值/零差值、极值与差值溢出自动锚点、缺测保留基线、
重置、无基线差分不可解、丢一个差分后直到锚点前不可确定、通道隔离、
1/2/3/5/7/64/1000 字节任意分块、半截帧 `ErrNeedMoreData`、坏 CRC/未知
tag/非最小 varint/语义坏帧一律 `ErrCorrupt` 且解码器永久失效、1GiB 虚假
长度额度在分配前被拒、定期锚点节奏、`StreamDecoder` 与半截流。
