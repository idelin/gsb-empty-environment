# framecomp

帧独立压缩容器（Go，仅标准库）。压缩器为本仓库自实现的 LZ77，
不调用任何现成压缩库；标准库仅用于 CRC-32、字节序等基础功能。

## 帧格式（小端）

| 字段    | 长度 | 含义                              |
|---------|------|-----------------------------------|
| magic   | 4    | `FCMP`，用于损坏后重新同步        |
| version | 1    | 格式版本，当前为 1                |
| method  | 1    | 0 = store，1 = LZ77               |
| compLen | 4    | 压缩后长度（字节）                |
| origLen | 4    | 原始长度（字节）                  |
| crc32   | 4    | **完整原文** 的 IEEE CRC-32       |
| payload | n    | compLen 字节压缩数据              |

## 解码器安全性质

- **损坏跳过**：候选帧只有在校验全部通过后才输出；坏帧不产生任何伪字节，
  解码器从下一字节重新扫描 magic 恢复后续帧。
- **伪长度受限**：`compLen` 同时受 `Limits.MaxFrameCompLen` 与流中实际剩余
  字节数约束，伪造长度不会导致越界读取或超额分配。
- **扫描预算**：重新同步每次最多扫描 `Limits.MaxScanBytes`；预算耗尽且
  后面仍有数据时返回 `ErrSyncLost`。
- **输出额度有界**：单帧解压上限 `MaxFrameOrigLen`，总输出上限
  `MaxTotalOutput`，超限返回 `ErrOutputQuota` / `ErrTooMuchOutput`。
- **CRC 只对完整原文**：解压结果必须恰好为 `origLen` 字节才计算并比对
  CRC；截断的帧永远无法通过校验。

## 用法

```sh
go build ./cmd/fcdemo
fcdemo compress <in> <out>   # 按 4 KiB 独立帧压缩
fcdemo extract <in> <out>    # 解码并跳过损坏帧
fcdemo demo                  # 内置“损坏-恢复”演示
go test ./...
```

## 运行样例

```
$ fcdemo demo
archive: 3 frames, 264 bytes
decode err=<nil> frames=2 out=2720 skipped=108 bad=2
recovered frames 1 and 3 exactly; corrupted frame 2 emitted nothing

$ fcdemo compress /tmp/sample.txt /tmp/sample.fc
compressed 23335 bytes into 6 frames (4693 bytes)
$ fcdemo extract /tmp/sample.fc /tmp/sample.out
decoded 6 frames, 23335 bytes out; skipped 0 bytes, rejected 0 bad candidates
$ cmp /tmp/sample.txt /tmp/sample.out && echo identical
identical
# 向归档中部写入 300 字节垃圾后：
$ fcdemo extract /tmp/sample.fc /tmp/sample.out2
decoded 4 frames, 15143 bytes out; skipped 1622 bytes, rejected 1 bad candidates
```

## 测试结果

`go test ./...`：18 个测试全部通过，覆盖 LZ77 往返/截断/输出上限/
非法距离、多帧往返、坏帧跳过与后帧恢复、伪长度（超限与超剩余数据）、
扫描预算、截断帧永不成功、CRC 语义、输出额度、帧间垃圾、版本拒绝、
头部字段布局。
