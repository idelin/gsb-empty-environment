# compact-segment-index

离线搜索客户端的紧凑索引段：TypeScript 实现，零依赖，不调用任何现成索引库。

- `src/writer.ts` — 段写入器：term → 递增文档ID（重复ID自动合并），分块差分编码。
- `src/reader.ts` — 查询器：单词查询、多词 AND 交集、基于跳转表的 `seek`
  （不会为一次 seek 解码此前所有块），并统计实际解码块数。
- `src/validator.ts` — 独立校验器：检查偏移重叠、整数溢出、非递增ID、摘要/CRC 损坏，
  问题报告携带文件字节区间与词项；只有校验通过的段才能打开查询。
- `FORMAT.md` — 完整二进制格式文档。

## 运行要求

Node ≥ 23.6（原生 TypeScript 类型擦除 + 内置 `node:test`），无需安装任何依赖。

```sh
npm test          # 等价于 node --test test/
```

## 用法

```ts
import { writeSegment } from './src/writer.ts';
import { validateSegment } from './src/validator.ts';
import { SegmentReader } from './src/reader.ts';

await writeSegment([['apple', [1, 3, 3, 5]], ['banana', [3, 4]]], 'seg.bin');

const report = await validateSegment('seg.bin');
console.assert(report.ok, report.issues);

const reader = await SegmentReader.open('seg.bin'); // 内部强制先校验
reader.get('apple');              // [1, 3, 5]
reader.intersect(['apple', 'banana']); // [3]
reader.stats.decodedBlocks;       // 实际解码的块数
```
