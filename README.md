# Arithmetic Coding (TypeScript)

高压缩率算术编码器：以 **大整数（BigInt）有理数** 精确维护当前状态区间 `[low, high)`，
每个符号按自适应概率模型的累计分布对区间做有理数细分，并通过 E1/E2/E3
重归一化（含 underflow 位）溢写位流，压缩率逼近香农熵极限。

## 结构

- `src/arithmetic.ts` — 63 位码空间上的 BigInt 区间编/解码器，有理数细分 `low + range * cum / total`，重归一化溢写位流
- `src/model.ts` — 二元自适应 order-0 概率模型（累计频率分布，编解码两端同步更新）
- `src/bitstream.ts` — MSB-first 位流读写器
- `src/demo.ts` — 运行样例：1,000,000 个 90% 为 0 的符号
- `test/arithmetic.test.ts` — Node.js 内置测试（`node:test`）

## 运行

需要 Node.js >= 22.6（原生 TypeScript 类型擦除）：

```sh
npm run demo   # 运行样例
npm test       # 运行测试
```

## 样例输出（1,000,000 符号，P(0) ≈ 0.9）

```
symbols:            1,000,000  (P(0) = 0.9001)
Shannon entropy:    0.4688 bits/symbol  ->  58,600 bytes (theoretical limit)
Huffman lower bound: 1.0000 bits/symbol  ->  125,000 bytes (binary alphabet)
arithmetic coded:   0.4688 bits/symbol  ->  58,604 bytes
vs Huffman bound:   53.1% smaller
round-trip:         LOSSLESS, exact termination
```

输出比二元霍夫曼编码理论下限（每符号 ≥ 1 bit）小约 53%，与香农熵极限仅差 4 字节。

## 测试结果

```
✔ round-trips binary streams of many lengths (exact termination)
✔ round-trips degenerate streams (all 0s / all 1s)
✔ round-trips across the full bias range
✔ acceptance: 90%-zero stream compresses far below the Huffman lower bound
✔ compression ratio tracks entropy for several biases
✔ decoding is deterministic and independent of trailing padding bits
ℹ tests 6  ℹ pass 6  ℹ fail 0
```

## 格式

`varint(符号数) || 算术编码位流`。解码端依据同步的累计概率分布反解，
解码满头部指定的符号数后精确终止。
