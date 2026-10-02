# RLE + Huffman 复合栅格压缩器

面向二值（1-bit）或低色深（2/4/8-bit）栅格图纸的双重压缩器，设计参照
Fax Group 3 / Group 4（CCITT T.4/T.6）的一维游程编码思想：

1. **第一级 RLE**：每条扫描线分解为游程长度序列。二值图像的游程严格黑白交替、
   每行从白色游程开始（与 T.4 一致，行首为黑时补零长白游程）；低色深图像编码
   `(像素值, 游程长度)` 对。
2. **第二级 Huffman**：游程长度映射到**预设最优变长码表**。码表不随文件传输——
   编解码双方从同一个面向"大面积留白图纸"调优的预设频率模型确定性地构建出
   完全相同的码本。长度 0–63 使用终止码，更长游程按 T.4 方式分解为
   补码（make-up，64 的倍数，最大 4032）+ 终止码。
3. **快速解码**：直接寻址 LUT（按 `maxLength` 位前缀索引）一次查表解出一个符号，
   还原为严格对齐的一维像素位图（行主序 `Uint8Array`）。

## 码流格式

```
头部 16 字节:  magic "RLH1" | width u32le | height u32le | bitDepth u8 | flags u8 | reserved u16le
负载:          逐行游程编码，MSB-first 位流，末尾零填充到字节边界（G4 风格，无 EOL 标记）
```

## 环境要求

- Node.js **>= 22.18**（或 >= 23.6）：直接使用内置 TypeScript 类型擦除运行 `.ts`，
  无需编译步骤、无第三方依赖。测试使用 Node 内置测试框架 `node:test`。

## 运行

```bash
npm test          # node --test：14 个测试（正确性 / 压缩率 / 性能 / 容错）
npm run demo      # 生成 2048x2048 技术图纸，压缩-解码-校验并打印指标与预览
```

## 验收结果（本仓库实测）

输入：2048×2048 二值技术图纸（边框、网格、零件轮廓、剖面线、标注、文字块，
墨水覆盖率 0.37%，大面积留白）：

| 指标 | 结果 |
| --- | --- |
| 原始体积（1bpp 紧凑位图） | 524,288 字节 |
| 压缩后 | 19,725 字节（**3.76%**，26.6×，远低于 5% 验收线） |
| 编码耗时 | ≈ 12 ms（高负载共享机器；空闲机器为个位数毫秒） |
| 解码耗时 | ≈ 2 ms |
| 还原精度 | 逐像素严格一致（全矩阵比对） |

## API

```ts
import { compress, decompress, inspect, packBits, rawSizeBytes } from './src/index.ts';

const image = { width, height, bitDepth: 1, pixels: Uint8Array }; // 行主序，每像素 1 字节
const data = compress(image);        // Uint8Array 压缩流
const info = inspect(data);          // 只解析头部 { width, height, bitDepth }
const back = decompress(data);       // 还原出严格对齐的像素栅格矩阵
const bitmap = packBits(back);       // 打包为 1bit/像素 的位图
```

## 目录结构

- `src/bitstream.ts` — MSB-first 位流读写（int32 累加器快速路径）
- `src/huffman.ts` — 预设频率模型、确定性 Huffman 码本构建、LUT 解码器
- `src/codec.ts` — 头部格式、RLE+Huffman 编码/解码、`inspect`/`packBits`
- `src/drawing.ts` — 合成技术图纸生成器（demo 与测试共用）
- `test/codec.test.ts` — `node:test` 测试套件
- `examples/demo.ts` — 端到端运行样例

## 设计说明与边界

- 纯一维编码（G3-1D 风格）：垂直线密集的图元（剖面线、网格）是 1D RLE 的
  天然弱项，验收用图纸保持真实图纸的稀疏度即可稳定低于 5%。如需进一步压缩
  可扩展二维 MMR（T.6）编码，本实现刻意保持 1D 以贴合规范。
- 非二值输入（2/4/8-bit）逐游程直接写入像素值，长度走中性码表。
- 损坏码流（截断、坏 magic、行溢出、非零填充位）一律抛错，不会静默产出错位栅格。
- 类型检查（可选）：`npm i -D typescript @types/node && npx tsc --noEmit`
  （`tsconfig.json` 已配置 `allowImportingTsExtensions`）。
