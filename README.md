# deflate-lite

纯标准库实现的轻量级 Deflate（RFC 1951）压缩内核，TypeScript 编写，
零第三方依赖。利用 Node.js ≥ 22.6 的原生 TypeScript 类型擦除直接运行，
无需编译步骤。

## 架构

两级流水线，与 RFC 1951 的分层设计一一对应：

```
Buffer ──▶ 阶段一 LZ77 ──▶ Token 流 ──▶ 阶段二 Huffman ──▶ Deflate 位流
            (lz77.ts)      literal|match   (huffman.ts + deflate.ts)
```

- `src/lz77.ts` — 32 KiB 滑动窗口 + 3 字节哈希链（`head`/`prev` 数组），
  惰性匹配（lazy matching）提取 `(distance, length)` 重复引用。
- `src/huffman.ts` — package-merge 算法构建限长（≤15 / ≤7 位）最优霍夫曼
  码长，规范码（canonical code）分配，以及 zlib `puff.c` 风格的
  count/offset 解码器。
- `src/deflate.ts` — 统计字面量/距离码频，构建动态霍夫曼表，码长序列经
  16/17/18 游程编码后写入块头；对 stored / fixed / dynamic 三种块格式
  逐位精确估价，选最省的一种输出。
- `src/inflate.ts` — 解压端，准确识别并还原三种块格式：
  非压缩块（BTYPE=00）、静态霍夫曼块（01）、动态霍夫曼块（10）。
- `src/bitstream.ts` — LSB-first 位流读写器（霍夫曼码 MSB 先写）。

## 运行

```bash
node --test          # 运行全部内置测试（node:test）
node examples/demo.ts  # 运行样例：1 MiB 系统日志压缩/解压 + SHA256 校验
```

要求 Node.js ≥ 22.6（开发验证使用 v23.9.0）。无需 `npm install`。

## 使用

```ts
import { deflate, inflate } from './src/index.ts';

const compressed: Buffer = deflate(rawData);
const restored: Buffer = inflate(compressed);
```

## 验收结果

### 运行样例（`node examples/demo.ts`）

1 MiB 含大量重复模式的合成系统日志（`examples/syslog.ts`，确定性种子）：

```
original:    1,048,653 bytes
sha256:      fad604b063c5d807a1fbc2a60761f145d98249835ab7a90ae944297b6b88df99
compressed:  103,267 bytes (9.8% of original) in 2034 ms
block type:  dynamic-huffman
restored:    1,048,653 bytes in 449 ms
sha256:      fad604b063c5d807a1fbc2a60761f145d98249835ab7a90ae944297b6b88df99
OK: SHA256 identical, byte-exact round trip
```

### 测试结果（`node --test`，10/10 通过）

```
✔ system log with heavy repetition: round trip + SHA256 match
✔ our dynamic/static output is decodable by node:zlib (inflateRaw)
✔ inflate handles dynamic Huffman blocks produced by zlib
✔ inflate handles static (fixed) Huffman blocks produced by zlib
✔ inflate handles stored (non-compressed) blocks produced by zlib
✔ incompressible data falls back to stored blocks and round trips
✔ edge cases: empty, single byte, uniform run, tiny text
✔ matches spanning the 32 KiB window boundary round trip
✔ all byte values and long literal runs round trip
✔ rejects corrupt streams
ℹ tests 10  ℹ pass 10  ℹ fail 0
```

覆盖要点：

- **三种块格式互通**：解压端分别验证 zlib 以 `Z_FIXED`（静态）、默认
  （动态）、`level: 0`（非压缩）策略产出的流；压缩端输出亦由
  `zlib.inflateRawSync` 交叉解码验证。
- **字节级一致**：每条用例还原后计算 SHA256 与原数据严格比对。
- **不可压缩回退**：随机数据自动选择 stored 块，开销 < 0.5%。
- **边界**：空输入、单字节、超 64 KiB 多 stored 块、跨 32 KiB 窗口
  边界的匹配、损坏流报错。
