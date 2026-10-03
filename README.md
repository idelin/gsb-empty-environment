# huffsync — 霍夫曼编码流损坏单点定位与错误扩散隔离器

面向不可靠信道的霍夫曼容错解码器（Go，仅标准库）。在位流中周期性插入具有
自同步特性的 32 位同步字（Sync Word）；当位流发生单比特反转或丢位时，解码器
在下一个同步字处重新锁定相位，把破坏隔离在单个段内。

## 设计

- **码表**：按消息字节频率构建霍夫曼码，额外保留伪符号 `SymInvalid`
  （频率 1，分得最长码字）。它永不出现在真实数据中，兼任段终止符与损坏
  检测点。码表带外传输。
- **帧格式**：`[SYNC 32bit][96 字节段]` 循环，同步字精确落在字节偏移
  0、100、200 …。每段打包完整码字并预留终止符空间，段尾写入
  `SymInvalid` 后零填充至 96 字节，保证段边界字节对齐。
- **自同步保证**：编码器发出前全流扫描，断言同步字只出现在帧边界，
  否则 panic（可更换同步字常量）。
- **容错解码**：先在（可能受损的）比特流中逐位扫描同步字定位段边界，
  再逐段独立解码。段内解码到 `SymInvalid`（正常终止）或比特耗尽（损坏
  尾部）即停。丢位/反转只污染所在段的尾部，下一个同步字处相位重新锁定，
  后续段完整恢复。丢位导致的整体比特偏移由同步字扫描天然吸收，不依赖
  绝对位置。

## 文件

- `bits.go` — 比特串与信道损伤模拟（`DeleteBits` / `FlipBit`）
- `huffman.go` — 霍夫曼码表构建与单符号编解码
- `stream.go` — 无帧基线编解码器（对照组）
- `resilient.go` — 同步字成帧编码器与容错解码器
- `resilient_test.go` — 验收测试
- `cmd/demo/main.go` — 运行样例

## 运行样例

```console
$ go run ./cmd/demo
message:            4000 bytes
channel damage:     erased 3 bits at byte 50

[ordinary decoder]  2356 bytes encoded, no framing
  correct symbols before losing phase: 86 / 4000
  everything after the erasure is garbage, e.g.:
  want: "How vexingly quick daft zebras jump! Bright vixe"
  got:  "dn! qykngly quick daft zebras jump! Bright vixen"

[resilient decoder] 25 frames of 100 bytes (32-bit sync word + 96-byte segment)
  segment 0 absorbed the damage and was truncated after 80/160 symbols
  re-locked at sync word originally at byte 100 (found at bit 797, ~byte 99, of the damaged stream)
  salvaged 3920 / 4000 symbols (98.00%)
  content after the byte-100 sync word is intact, e.g.:
  "The quick brown fox jumps over the lazy dog. Pack my box with fi"
```

## 测试结果

```console
$ go test ./... -v
=== RUN   TestRoundTripNoCorruption
--- PASS: TestRoundTripNoCorruption (0.00s)
=== RUN   TestSyncWordsLandOnHundredByteGrid
--- PASS: TestSyncWordsLandOnHundredByteGrid (0.00s)
=== RUN   TestResyncAfterThreeBitErasure
--- PASS: TestResyncAfterThreeBitErasure (0.00s)
    resilient_test.go: ordinary decoder: 86 correct symbols then garbage (tail match 0.0%)
    resilient_test.go: re-locked at sync word originally at byte 100 (bit 797 of damaged stream)
    resilient_test.go: salvaged 98.00% of 4000 symbols
=== RUN   TestResyncAfterSingleBitFlip
--- PASS: TestResyncAfterSingleBitFlip (0.00s)
    resilient_test.go: bit flip: salvaged 97.97%
PASS
ok  	huffsync
```

验收边界对照：抹去 3 个 bit 后，普通解码器仅前 86 个符号正确、后续全部
乱码（位置匹配率 0.0%）；容错解码器跳过损坏符号，在原第 100 字节处的同步字
（受损流中因丢位前移至 bit 797）重新同步，挽救 98.00% ≥ 90% 的有效内容。
