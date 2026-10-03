# deflate-core

纯标准库实现的轻量级 Deflate（RFC 1951）压缩内核，TypeScript 编写，无任何第三方依赖。

## 流水线

**第一阶段 · LZ77（`src/lz77.ts`）**
- 32 KiB 滑动窗口，3 字节乘法哈希表（`head`/`prev` 链式数组，zlib 风格）
- 惰性匹配（lazy matching）：仅当下一个位置无法产生更长匹配时才输出当前匹配
- 输出 `(Distance, Length)` 引用或字面量 token 流

**第二阶段 · Huffman（`src/huffman.ts` + `src/deflate.ts`）**
- 对字面量/长度（286 码）与距离（30 码）两个字母表分别统计频率
- 堆构建霍夫曼树，超限深度按 zlib `gen_bitlen` 算法重分配，保证 ≤15 位
- 码长序列经 RLE（16/17/18）压缩后再用 7 位上限的码长霍夫曼树编码
- 逐流比较 stored / fixed / dynamic 三种块编码的精确位数，选最小者输出

**解压端（`src/inflate.ts`）**
- 完整识别三种块格式：非压缩块（0）、静态霍夫曼块（1）、动态霍夫曼块（2）
- 规范霍夫曼码逐位解码，校验 LEN/NLEN、距离越界、非法码长等错误

## 运行

需要 Node.js ≥ 22.6（利用内置 TypeScript 类型擦除，无需编译步骤）：

```bash
npm test    # node:test 内置测试
npm run demo
```

## 使用

```ts
import { deflate, inflate } from "./src/index.ts";

const { data: compressed, blockType } = deflate(original); // Uint8Array -> Uint8Array
const restored = inflate(compressed);                      // 逐字节还原
```

## 验证

- 9 组数据夹具（空输入、单字节、64K 重复字节、随机 200K、合成系统日志、
  窗口边界重复等）往返后 SHA256 与原数据严格一致
- 编码输出经 Node 参考实现 `zlib.inflateRawSync` 交叉验证
- 解压端分别验证 zlib 产生的动态（level 9）、静态（`Z_FIXED`）、
  非压缩（level 0）三种块格式
- 霍夫曼码长满足 Kraft 等式且不超过位宽上限；损坏流正确抛错

样例结果（约 400 KiB 重复模式系统日志）：压缩比 0.088，动态霍夫曼块，
SHA256 往返一致，zlib 交叉校验通过。
