# tANS-FSE — 有限状态熵编码器（TypeScript）

不对称数字系统（Asymmetric Numeral Systems, ANS）的表格化变体 **tANS** 的零依赖
TypeScript 实现：压缩率媲美算术编码（贴近香农熵理论界限），吞吐达到经典算术编码的
数倍，接近霍夫曼编码的流水线速度。

## 核心思想

- **单整数状态**：整个压缩状态折叠进一个整数 `state`（编码侧 `tableSize ≤ state <
  2·tableSize`）。`src/bigintAns.ts` 用单个 `BigInt` 演示了这一性质的纯粹形式——
  整条消息就是一个大整数；`src/tans.ts` 在此基础上加入重整化（renormalization），
  把溢出的低位比特流入字节流，使状态有界、可流式工作。
- **离散状态转移查找表**：预先生成三张解码表（`dSymbol` / `dNbBits` / `dNewState`）
  与编码表（`nextState` + 每符号常量 `deltaNbBits` / `deltaFindState`），
  每次 Push/Pop 都是 O(1) 的查表 + 少量位运算：

  ```
  encode: nbBits = (state + deltaNbBits[s]) >> 16
          emit low nbBits of state
          state  = nextState[(state >> nbBits) + deltaFindState[s]]

  decode: s     = dSymbol[state]
          state = dNewState[state] + readBits(dNbBits[state])
  ```

- **LIFO 位流**：编码器反向压入符号、正向写出比特；解码器从流尾反向读比特
  （`src/bitstream.ts`，末尾带 1-bit 结束标记），因此解码按原始顺序弹出符号。

## 文件结构

```
src/
  bitstream.ts   LSB-first 位写器 / 反向位读器（结束标记）
  normalize.ts   频率统计与归一化（最大余数法，和恰为 2^tableLog）
  table.ts       tANS 状态转移表构建（奇数步长散布 + FSE 式建表）
  tans.ts        TansEncoder/TansDecoder 流式 API + compress/decompress 自封包格式
  bigintAns.ts   单个大整数状态的纯 ANS（无重整化，教学/验证用）
  arithmetic.ts  经典 32 位区间算术编码器（吞吐/压缩率对照基线）
  entropy.ts     香农熵与整数码长界限计算
test/            Node.js 内置测试（node:test）：正确性、压缩率、吞吐基准
examples/demo.ts 端到端运行样例
```

## 运行

要求 Node.js ≥ 23.6（内置 TypeScript 类型擦除，零依赖、零构建；
Node 22.6+ 可加 `--experimental-strip-types` 运行）。

```bash
npm test          # 等价于 node --test
npm run demo      # 等价于 node examples/demo.ts
```

## 实测结果（Apple Silicon，Node v23.9，4 MB 高偏斜 Zipf 样本）

```
sample            : 4.00 MB highly skewed i.i.d. (Zipf, 48 symbols)
entropy bound     : 3.3054 bits/symbol -> 1.65 MB (optimal)
Huffman-class     : 3.8967 bits/symbol -> 1.95 MB (integer code lengths)

tANS (tableLog=12): 1.65 MB = 3.3060 bits/symbol
  vs entropy bound: +0.017% (incl. header)
  throughput      : encode 45.7 MB/s, decode 54.0 MB/s

arithmetic coder: 1.65 MB payload (same static model)
  throughput      : encode 14.5 MB/s, decode 14.1 MB/s
  tANS speedup    : encode 3.1x, decode 3.8x
```

- 压缩率：距理论熵界限仅 +0.017%（含头部），明显优于整数码长的霍夫曼类编码。
- 吞吐：表驱动解码达到逐比特串行算术解码器的 ~4 倍。
- 测试套件（20 项）覆盖：往返一致性（空输入/单符号/全字母表/均匀随机/文本/
  多档 tableLog）、状态表不变量、压缩率 vs 熵界限、与算术编码器的压缩率对等性、
  大整数状态精确互逆、吞吐基准门槛。
