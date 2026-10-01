# arithmetic-coding

基于概率分布的算术编码（Arithmetic Coding）压缩器，TypeScript 实现，零运行时依赖。

## 原理

- **大整数有理数区间状态**：当前区间 `[Low, High)` 以 BigInt 不动点有理数维护，
  状态值为共享分母 `2^PRECISION`（默认 512 bit）的分子，远超经典 32/64 位实现的精度，
  码长与香农熵极限的差异可忽略（实测开销 +0.056%）。
- **区间细分**：每个符号按概率模型的累计分布把 `[low, high)` 细分到
  `[cumLow/total, cumHigh/total)` 子区间（`src/arithmetic.ts` 中 `encode`/`update`）。
- **重归一化**：E1/E2/E3 规则——区间完全落入下半区 / 上半区 / 中间半区时，
  溢写（或挂起 pending 位）一个比特并把区间放大回 `[0, 1)`，下溢位采用
  bit-plus-follow 机制延迟输出。
- **精确终止**：码流头部以 varint 记录符号个数，解码端按同一累计概率分布反解，
  输出恰好 `symbolCount` 个符号后停止（对尾部垃圾字节免疫）。

## 码流格式

```
varint  symbolCount        解码符号数（精确终止依据）
varint  alphabetSize
alphabetSize × { byte symbol, varint frequency }   概率模型（自包含）
...     算术编码位流（MSB 优先，末尾零填充）
```

## 使用

```bash
npm run build   # TypeScript -> dist/（零依赖类型擦除构建）
npm test        # 构建并运行测试（Node.js 内置测试）
npm run demo    # 运行压缩率演示
```

API：

```ts
import { FrequencyModel } from "./src/model.js";
import { compress, decompress } from "./src/codec.js";

const model = new FrequencyModel([0, 1], [9, 1]); // P(0)=0.9, P(1)=0.1
const packed = compress(data, model);             // Uint8Array -> Uint8Array
const back = decompress(packed);                  // 精确还原
```

## 运行样例（`npm run demo`，1,000,000 个符号、90% 为 0）

```
=== Arithmetic coding demo (BigInt rational interval, 512-bit state) ===
symbols              : 1000000  (actual P(0) = 0.8999)
model                : P(0) = 9/10, P(1) = 1/10 (static, sent in header)
raw size             : 1000000 bytes (1 byte/symbol)
Huffman lower bound  : 125000 bytes (1 bit/symbol)
Shannon entropy limit: 58624.4 bytes (0.4690 bits/symbol)
arithmetic coded     : 58657 bytes (header included)
  vs Huffman bound   : 53.1% smaller
  vs entropy limit   : +0.056% overhead
roundtrip            : OK (1000000 symbols decoded exactly)
```

对独立同分布二元流，霍夫曼编码理论下限为 1 bit/符号（125000 字节）；
算术编码输出 58657 字节，**比霍夫曼下限小 53.1%**，距香农熵极限仅 +0.056%。

## 测试结果（`npm test`，12 项全部通过）

```
ok 1 - bitstream: bit-level write/read roundtrip incl. partial byte
ok 2 - model: cumulative distribution and binary search
ok 3 - codec: empty input roundtrip
ok 4 - codec: single repeated symbol needs ~zero payload bits
ok 5 - codec: roundtrip of 90%-zero binary streams at many sizes
ok 6 - codec: roundtrip with data-derived model
ok 7 - codec: beats the Huffman lower bound and approaches Shannon entropy
ok 8 - codec: decoder terminates exactly after symbolCount symbols
ok 9 - codec: encoding is deterministic
ok 10 - codec: generic byte alphabet roundtrip (text)
ok 11 - codec: full 256-symbol alphabet roundtrip
ok 12 - arithmetic: precision constant is large enough for the model
# tests 12 | pass 12 | fail 0
```

## 目录结构

- `src/bitstream.ts` — 位级读写（MSB 优先，越界读返回 0 填充）
- `src/model.ts` — 静态频率模型 / 累计概率分布 / 二分查找
- `src/arithmetic.ts` — BigInt 区间状态、区间细分、E1/E2/E3 重归一化
- `src/codec.ts` — 自包含码流格式（头 + 位流）的压缩 / 解压
- `test/arithmetic.test.ts` — Node.js 内置测试（`node:test`）
- `test/harness.ts` — Node < 18 环境下的内置测试回退执行器
- `examples/demo.ts` — 压缩率演示
- `scripts/strip-types.mjs` — 零依赖构建：TS 类型擦除 → `dist/`（`node --check` 校验）
- `scripts/run-tests.mjs` — 测试入口

## 备注

- 测试基于 Node.js 内置模块：Node ≥ 18 时可直接 `node --test dist/test/`；
  更低版本由 `test/harness.ts` 的内置回退 runner 执行同一套件。
- 源码采用可擦除的 TypeScript 子集（无枚举 / 参数属性 / 断言），
  因此无需任何 npm 依赖即可构建运行；有完整工具链时亦可直接 `tsc -p tsconfig.json`。
