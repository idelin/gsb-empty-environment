# LZH1：独立块日志归档压缩

自定义 **LZ77 + 规范霍夫曼** 压缩（不与 DEFLATE 兼容）。每个块自描述、
可独立解码、独立校验，适合日志归档场景中"坏一块不连坐"的需求。

## 运行环境

- Node ≥ 23.6（原生 TypeScript 类型擦除），或 Node 22.6+ 加
  `--experimental-strip-types`。无第三方依赖。
- `npm test` 会自动在 nvm 目录中寻找满足要求的 Node。

## 块格式

见 [FORMAT.md](FORMAT.md)：每块声明 **原始长度、窗口上限、码长表、
压缩载荷长度** 与 CRC-32 校验；位序（字节内 LSB-first、码字 MSB-first、
整数额外位 LSB-first）也在其中定义。

要点：

- 匹配距离 1..已产生历史，且 ≤ 窗口上限；**长度可大于距离**，
  按重叠复制语义逐字节展开。
- 规范码按 **(码长, 符号)** 排序建立；解码器拒绝过度订阅码，
  允许欠订阅码（含"全表只有一个符号"）。
- 解码器拒绝：非法距离、展开超过声明长度、截断、CRC 不符、
  载荷长度不符、非零填充位。

## 库用法

```ts
import { encodeBlock, decodeBlock, BlockDecoder } from './src/index.ts';

// 一次性
const block = encodeBlock(data, 65535);   // windowLimit 1..65535
const back = decodeBlock(block);

// 流式：任意字节边界分块输入，输出受调用方额度限制
const dec = new BlockDecoder();
dec.push(chunk1); dec.push(chunk2);       // 任意分块
const { output, done } = dec.decode(4096); // 单次最多输出 4096 字节
// 额度或输入耗尽时暂停，下次从断点继续，绝不重复输出；
// 输入结束时调用 dec.finish()，截断会抛 FormatError。
```

## 命令行

```sh
node src/cli.ts compress   输入 输出 [--window 65535] [--block-size 262144]
node src/cli.ts decompress 输入 输出
node src/cli.ts stats      压缩文件
```

CLI 把输入切成固定大小的独立块串联写出；`stats` 输出块数、原始/压缩
字节数与比例。**大小统计仅供参考，压缩率下降不是正确性证明**——
正确性由测试保证（见下）。

## 测试

```sh
npm test          # 或: node --test test/*.test.ts
```

- `test/roundtrip.test.ts`：穷举小串往返（二元字母表 ≤ 12、三元 ≤ 8，
  共 1.8 万例）；随机数据多字母表/多窗口往返；任意分块 + 额度组合的
  流式解码；窗口上限 1。
- `test/reference-expander.ts`：**不使用压缩器匹配逻辑** 的 token 参考
  展开器，所有往返用例同时用它交叉验证 token 序列。
- `test/format.test.ts`：距离 1 长重复（重叠语义）、空块、单符号码表、
  **逐字节截断**、声明长度溢出（改小/改大/置零/置 0xFFFFFFFF）、
  过度订阅码表、非法距离（0 / 超历史 / 超窗口）、CRC 翻转、
  载荷与码长表损坏注入、填充位非零、载荷长度虚报、
  损坏块不污染下一独立块、额度暂停后不重复输出。
- `test/huffman.test.ts`：规范码分配、过度订阅拒绝、欠订阅接受、
  斐波那契极端频率限长、随机频率位级往返。
- `test/cli.test.ts`：CLI 端到端往返与损坏文件非零退出。

## 目录

```
src/bitstream.ts  位流写入（LSB-first）
src/crc32.ts      CRC-32 (IEEE)
src/huffman.ts    限长霍夫曼码长构建、规范码、增量解码
src/lz77.ts       哈希链贪心分词（仅编码端）
src/block.ts      块头、编码、流式解码器
src/cli.ts        命令行与大小统计
test/             node:test 测试
FORMAT.md         格式与位序权威定义
```
