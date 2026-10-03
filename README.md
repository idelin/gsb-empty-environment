# RLE + Huffman Compound Raster Compressor

面向二值（1 bit/px）或低色深栅格图纸的双重压缩器，TypeScript 实现，零运行时依赖。

- **第一级 RLE**：逐行提取连续同色像素的游程长度（Fax 惯例：每行从白色游程开始，行首为黑则补 0 长度白游程）。
- **第二级 Huffman**：游程长度映射到**预设**的最佳变长码表（类 Fax Group 3/4 的 Modified Huffman：终止码 0–63 + 补偿码 64–8192，超过 8192 走转义码 + 32 位字面量）。白/黑游程使用两张独立的预设码表，码表由固定的图纸频率模型离线生成，无需随码流存储。
- **解码**：按位流顺序重建一维像素位图（行优先 `Uint8Array`，`pixels[y * width + x]`），栅格严格对齐。

## 码流格式

```
[0..3]   magic "RHC1"
[4..7]   width   (uint32 LE)
[8..11]  height  (uint32 LE)
[12..]   MSB-first 位流：逐行 Huffman 编码的游程长度
```

解码器已知行宽，每行累计游程达到宽度即换行，无需行结束标记；游程溢出会抛出异常以拒绝损坏码流。

## 环境要求

- Node.js **>= 22.18**（或 >= 24）：直接以原生类型擦除运行 `.ts` 源码，无需构建步骤。
- 可选：`npm i -D typescript @types/node` 后可用 `npm run typecheck` 做类型检查（仓库已附 `tsconfig.json`）。

## 运行

```bash
npm test          # Node.js 内置测试运行器（node:test）
npm run demo      # 生成技术图纸样例并验证验收指标
```

## 使用示例

```ts
import { compress, decompress } from './src/compressor.ts';
import { createBitmap } from './src/bitmap.ts';

const bmp = createBitmap(3508, 2480);   // 0 = 白, 1 = 黑, 行优先一维数组
bmp.pixels[100 * bmp.width + 200] = 1;  // 画一个黑点

const packed = compress(bmp);           // Uint8Array，含 RHC1 头
const restored = decompress(packed);    // { width, height, pixels } 严格对齐
```

## 验收指标（样例图纸 3508 × 2480，A4 @300dpi）

| 指标 | 结果 |
| --- | --- |
| 原始体积（1 bit/px 打包） | 1.04 MiB |
| 压缩后 | 48.83 KiB（**4.6%**，目标 < 5%） |
| 相对 8 bit/px 原始体积 | 0.575% |
| 压缩 / 解压耗时 | 空闲机器上约几毫秒 ~ 几十毫秒（共享高负载环境下会放大） |
| 往返一致性 | 逐像素严格相等 |

## 目录结构

```
src/bitstream.ts    MSB-first 位流读写
src/huffman.ts      霍夫曼码长构建、规范码分配、解码树
src/tables.ts       类 Fax G3/G4 预设白/黑码表（终止码 + 补偿码 + 转义码）
src/rle.ts          游程提取 / 还原（基于原生 indexOf 的快速扫描）
src/bitmap.ts       二值位图类型与工具
src/compressor.ts   复合压缩器入口（compress / decompress）
examples/drawing.ts 合成技术图纸生成器（确定性）
examples/demo.ts    验收演示
test/               node:test 测试（27 个用例）
```
