# tANS 有限状态熵编码器（TypeScript）

不对称数字系统（Asymmetric Numeral Systems, tANS 变体）熵编码内核：
结合算术编码的压缩率与霍夫曼编码的吞吐。整个压缩状态是**单个大整数**
`x ∈ [M, 2M)`（`M = 2^tableLog`），压入/弹出符号均为 O(1) 查表加一次
变长比特冲刷。

## 原理

- **状态转移表**（`src/table.ts`）：频率归一化到 `2^tableLog` 后，符号按
  FSE 风格步长 `(M>>1)+(M>>3)+3` 散布到 M 个槽位（散布消除连续分配的
  ~0.04 bit/sym 结构性冗余）。符号 `s` 的第 `j` 次出现对应前驱基
  `y = f_s + j`，回读位数 `nb = tableLog + 1 - bitLength(y)`；区间
  `[y<<nb, (y+1)<<nb)`（`y ∈ [f, 2f)`）恰好铺满 `[M, 2M)`，使编码器成为
  解码器的精确逆（测试中有全状态×全符号穷举证明）。
- **编码**（`src/tans.ts`）：`push` 冲刷 `nb ∈ {shift-1, shift}` 比特使
  `x >> nb ∈ [f, 2f)`，然后一次查表跳转状态；`finish` 写出最终状态
  （tableLog 比特）。ANS 是栈结构：反向编码、正向解码。
- **解码**：`pop` 一次槽位查表 + 回读 `nb` 比特，流水线速度。
- **容器格式**（`src/codec.ts`）：自描述头部（tableLog、频率表、原始
  长度、载荷比特数）+ 载荷。
- **对照基线**（`src/arithmetic.ts`）：同概率模型的经典 32 位算术编码器，
  用于吞吐与压缩率对比。

## 运行

要求 Node.js ≥ 22.6（原生 TypeScript 类型擦除，无需编译）。

```bash
npm test          # node --test：12 项测试
npm run demo      # 高偏斜样本压缩演示
npm run bench     # tANS vs 算术编码吞吐基准
```

## 测试结果（node --test）

```
✔ bitstream: backward write/read roundtrip
✔ bitstream: forward read matches write order
✔ table: frequencies normalise to exactly 2^tableLog, min 1
✔ tans: push/pop roundtrip on skewed sample
✔ tans: state stays within [M, 2M) — single big-integer state
✔ tans: encode/decode are exact inverses for every state x symbol
✔ tans: compression approaches Shannon bound on skewed input
✔ codec: full container roundtrip incl. header
✔ codec: edge cases
✔ codec: deterministic output
✔ arithmetic baseline: roundtrip with same model
✔ tans: interleaved encoder/decoder manual walk
ℹ tests 12 / pass 12 / fail 0
```

## 运行样例（npm run demo，1MB 高偏斜输入）

```
input:            1,000,000 bytes (skewed, 32-symbol alphabet)
shannon entropy:  2.4267 bits/symbol
compressed:       303,897 bytes  (ratio 3.29x)
achieved:         2.4312 bits/symbol (incl. header)
gap to entropy:   0.18%
roundtrip ok:     true
```

## 基准（npm run bench，2MB 高偏斜输入，同概率模型）

```
            encode MB/s   decode MB/s   output bytes
tANS              67.5          50.6        607,320
arithmetic        11.6          11.0        607,320

speedup:    encode 5.8x   decode 4.6x
size delta: 0.000% vs arithmetic coder
```

tANS 与算术编码输出字节数完全一致（差距 < 0.001%），吞吐为其数倍；
与香农熵界限的差距约 0.18%（含频率表头开销）。

## 文件结构

```
src/bitstream.ts   LSB 优先比特流（写正向、读可正可逆——ANS 栈序）
src/table.ts       频率归一化 + 散布 + 状态转移表构建
src/tans.ts        TansEncoder.push / TansDecoder.pop（O(1) 流式）
src/codec.ts       自描述容器 compress/decompress + 熵分析
src/arithmetic.ts  算术编码对照基线
test/tans.test.ts  node:test 测试套件
examples/demo.ts   压缩演示
examples/benchmark.ts  吞吐基准
```
