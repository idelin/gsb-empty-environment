# runlzv2

一个只用 Go 标准库实现的游程长度（RLE）块压缩格式与编解码器，
提供**离线最优**与**在线有界缓冲**两种模式，支持显式流式 flush。

## 线格式（版本 1）

```
帧头（固定 10 字节）:
  version   u8   = 1
  flags     u8   = 0x01 Flush | 0x02 Final
  length    u32be        载荷字节数
  crc32     u32be        载荷的 CRC-32/IEEE

块头（固定 4 字节）:
  tag       u8   = 0x00 字面块 | 0x01 重复块
  len-1     u16be        块长减 1（1..65535）
  value     u8           重复块的字节值（字面块为 0）

字面块载荷: len 个原始字节
重复块载荷: 无（value 重复 len 次，len >= 2）
```

- 空载荷帧必须带 Flush 或 Final 标志；空非标志帧非法。
- 空输入离线编码返回**无帧**（0 字节），与显式空 flush 帧可区分。
- Flush 是显式边界（解码端表现为一次 `Read` 返回 `(0, nil)`），**不等于流结束**；
  只有 Final 帧产生 `io.EOF`。

## 块切分：真实头部成本的最小总字节 DP

`planOptimal` 以后缀 DP 最小化总字节数，每块真实成本为
`BlockHeaderLen + len`（字面）或 `BlockHeaderLen`（重复），
块长上限 65535。用两个单调队列做到 O(n) 时间、O(n) 空间。
短重复不会误吞相邻字面：重复块必须落在同一游程内，
且仅当总字节更优（平局优先）时才选择。

## 两种模式与在线差距

- **离线最优** `Encode(src)`：整段输入做一次最优 DP，单 Final 帧。
- **在线有界缓冲** `Encoder`：输入缓冲上限 `bufferSize`（默认 4096，
  最大 65535）。缓冲满或 `Flush` 时立即对当前缓冲做最优 DP 并发帧，
  不假设未来输入。

在线差距上界：每个强制边界（缓冲满或 flush）最多多出一个帧头加一个
块头，即 `gap <= boundaries * (FrameHeaderLen + BlockHeaderLen)` = 每边界
14 字节（测试 `TestOnlineGapBound` 验证）。

最坏膨胀有界：`len(encoded) <= FrameHeaderLen + n + ceil(n/65535) * BlockHeaderLen`
（全字面块情形，测试 `TestWorstCaseExpansionBound` 验证）。

## 解码

- `Reader` 流式解码，输出额度可由 `NewReaderLimit` 限制（`ErrOutputLimit`）。
- 小缓冲区读取即背压；恢复读取不会重复字节（`TestBackpressureNoDuplicateBytes`）。
- 版本、标志、块标签、长度、CRC 全部校验；截断流报错。

## 运行

```sh
go test ./...
go run ./cmd/demo
```

示例输出：

```
offline: input=133 encoded=55 roundtrip=true
online:  input=133 encoded=107 roundtrip=true gap=52 bytes (buffer=32, 1 flush)
empty:   offline-bytes=0 (no frame) stream-bytes=20 (flush+final frames)
```
