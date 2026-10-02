# Huffman 容错解码器：同步字重同步与错误扩散隔离

面向不可靠信道的霍夫曼容错解码器（TypeScript，Node.js 原生 TypeScript 运行，零依赖）。

## 原理

- 位流被切分为 **100 字节的固定段**，每段以 32 位自同步同步字 `0x1ACFFC1D` 开头，
  随后是霍夫曼编码符号、EOS 标记和零填充。
- **普通解码器**信任固定段布局：一旦发生丢位，后续所有段边界整体错位，输出全部乱码。
- **容错解码器**在任意比特偏移上扫描同步字：当前段解码失败（遇到非法码字或
  段内 800 比特内未出现 EOS）时，从上一同步字之后重新扫描，在下一个同步字处
  重新锁定相位，跳过损坏符号，把破坏隔离在单个段内。

## 文件

- `src/huffman.ts` — 霍夫曼码表构建、解码 trie、单符号解码
- `src/codec.ts` — 分帧编码器、普通解码器、容错解码器、同步字扫描
- `src/bitstream.ts` — 比特级工具（删位 / 翻转比特，模拟信道损伤）
- `src/sample.ts` — 演示用消息与码表
- `src/demo.ts` — 运行样例
- `test/codec.test.ts` — Node.js 内置测试（`node:test`）

## 运行

```sh
npm run demo   # 运行样例
npm test       # 运行测试（node:test，Node >= 23 直接运行 .ts）
```

## 运行样例（`npm run demo`）

构造 2652 个符号的霍夫曼位流（1500 字节，15 个段），在第 50 字节处故意抹去 3 个 bit：

```
=== Fault-tolerant Huffman stream demo ===
message: 2652 symbols, encoded: 1500 bytes, segments of 100 bytes, sync words at bytes 0, 100, 200, 300...

channel: deleted 3 bits at byte 50 (bit 400)

[naive decoder]  decoded 2644 symbols
  head: "the quick brown fox jumps over the lazy dog while the rain k"
  tail after corruption: "eeps falling on the quu t netoe nd the river carries the sound of the evening far away toward the se"

[fault-tolerant decoder] decoded 2652 symbols, resync events: 0
  re-locked on sync word at corrupted-stream bit 797 (original byte 100, i.e. the sync word at byte 100)

  symbols intact before corruption: 82
  corrupted symbols skipped:        101
  symbols recovered after resync:   2469 / 2570 post-corruption symbols
  recovery rate:                    96.1%
  recovered tail identical to original: true

  naive decoder post-corruption exact-match rate: 9.6% (rest is garbled/misaligned)
```

普通解码器损坏点后逐符号精确匹配率仅 9.6%（后续全部乱码/错位）；容错解码器跳过
101 个损坏符号后，在第 100 字节处的同步字（受损流中移至 bit 797）重新锁定，
后续 2469 个符号与原文逐位一致，挽救 96.1% 的受损后内容（≥ 90% 验收线）。

## 测试结果（`npm test`）

```
✔ clean stream round-trips exactly through the tolerant decoder
✔ sync words sit at 100-byte boundaries and are found by the bit-level scanner
✔ 3-bit deletion: naive decoder garbles everything after the damage
✔ 3-bit deletion: tolerant decoder re-locks on the sync word at byte 100
✔ 3-bit deletion: tolerant decoder salvages >= 90% of post-corruption content
✔ single-bit flip: damage is isolated to one segment
✔ corruption inside a later segment still resyncs at the next sync word
✔ sync word never appears by accident inside a clean stream's data
ℹ tests 8  ℹ pass 8  ℹ fail 0
```
