# huffsync — 霍夫曼容错解码器（同步字重锁定）

面向不可靠信道的霍夫曼容错解码器：在编码流中周期性插入具有自同步特性的
同步字（Sync Word）。当位流发生单比特反转或丢位时，解码器在下一个同步字
处重新锁定相位，把破坏隔离在失锁区间内。

## 帧格式（定长 50 字节，字节对齐）

```
+----------------+----------------+-----------------------------+
| sync word (4B) | symbol count |  Huffman payload (44 B,       |
| A5 5A A5 5A    | (2 B, BE)    |  zero-padded to byte)         |
+----------------+----------------+-----------------------------+
```

同步字因此固定落在编码流的字节 0、50、100、150、… 处。

## 工作原理

- **编码**（`EncodeFrames`）：按符号频率构建 Huffman 码，把符号装入 44 字节
  负载预算的定长帧，帧头为同步字 + 符号计数。
- **普通解码器**（`DecodeNaive`）：盲目信任帧结构，既不校验同步字也不恢复，
  一旦丢位导致相位偏移，后续全部乱码。
- **容错解码器**（`DecodeFaultTolerant`）：每帧解码后校验下一同步字；失锁时
  从上一帧之后逐比特扫描，并用"计数字段合理性 + 负载预算"做假同步字排除，
  在下一个真实同步字处重新锁定。丢位会使同步字前移，因此扫描起点回退一个
  帧长，覆盖前移情形。

## 运行样例（`go run .`）

```
=== Huffman fault-tolerant decoder demo ===
source text : 2158 bytes, 27 distinct symbols
encoded     : 26 frames x 50 B = 1300 B (sync word every 50 B, at bytes 0, 50, 100, ...)
corruption  : deleted 3 bits at byte 60 (bits 480-482)

--- naive decoder (no resync) ---
prefix OK   : "Huffman coding assigns short codes to frequent symbols; a resilient deco..."
then garbage: " sync wae s so it can re-lock at the next marker after a bit error on th..."
post-corruption content match: 12.2%

--- fault-tolerant decoder ---
  bit   800  sync-lost  sync word mismatch; scanning forward
  bit   797  resync     re-locked on sync word  <-- sync word written at byte 100 (shifted to bit 797 by the 3 deleted bits)
resynced at the sync word of byte 100; everything from frame #2 on is intact: true
salvaged    : 1990 of 2075 post-corruption symbols = 95.9%
recovered tail: "nc words so it can re-lock at the next marker after a bit error on the c..."
```

## 测试结果（`go test -v ./...`）

```
=== RUN   TestRoundTrip
--- PASS: TestRoundTrip (0.00s)
=== RUN   TestSyncWordPlacement
--- PASS: TestSyncWordPlacement (0.00s)
=== RUN   TestNaiveDecoderGarbledAfterDeletion
--- PASS: TestNaiveDecoderGarbledAfterDeletion (0.00s)
=== RUN   TestFaultTolerantResyncAfterDeletion
--- PASS: TestFaultTolerantResyncAfterDeletion (0.00s)
    huffsync_test.go:81: salvaged 95.9% of post-corruption content
=== RUN   TestSingleBitFlipContained
--- PASS: TestSingleBitFlipContained (0.00s)
    huffsync_test.go:104: bit flip corrupted 1 of 2158 symbols
PASS
ok  	huffsync
```

## 验收边界对应

- 在字节 60 处抹去 3 个 bit：普通解码器后续内容匹配率仅 12.2%（乱码）。
- 容错解码器在写入于第 100 字节的同步字处重新锁定（丢位后移至 bit 797）。
- 跳过损坏符号后，挽救了损坏点之后 95.9% 的有效内容（≥ 90%）。
- 单比特反转不破坏相位，损伤被隔离在 1 个符号内。

## 文件

- `bits.go` — MSB-first 位流读写器
- `huffman.go` — Huffman 码构建（确定性堆序）与解码字典树
- `frame.go` — 帧协议、普通/容错解码器、丢位注入工具
- `main.go` — 演示程序
- `huffsync_test.go` — `go test` 验收测试
