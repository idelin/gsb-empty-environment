# compact-segment-index

离线搜索客户端的紧凑索引段：写入器、查询器、独立校验器。纯 TypeScript，
仅依赖 Node 内置模块，不使用任何现成索引库。二进制格式见 `FORMAT.md`。

## 模块

- `src/format.ts` — varint（LEB128，u32 溢出检查）、CRC-32、FNV-1a 词项哈希
- `src/writer.ts` — `SegmentWriter`：输入 term → 文档 ID 列表，重复 ID 合并，
  排序后分块差分编码，生成词典 + posting 块 + 块跳转表 + CRC 摘要
- `src/validator.ts` — `validateSegment(buf)`：独立校验，检查偏移重叠、整数
  溢出、非递增 ID、摘要损坏等；每个错误带文件字节区间与词项
- `src/reader.ts` — `SegmentReader`：仅在校验通过后提供查询；单词查询、
  多词 AND 交集、游标 `seek(id)`（借助跳转表，不解码此前所有块）
- `demo.ts` — 构建/查询演示，打印实际解码块数

## 运行

需要 Node ≥ 23（原生类型擦除）或 Node ≥ 22.6 加 `--experimental-strip-types`：

```sh
node --test test/segment.test.ts   # 运行全部测试
node demo.ts                       # 运行演示
```

## 测试覆盖

空词项与空 posting 列表、最大合法 ID（0xFFFFFFFF）、跨块 AND 交集、
每个字节位置的截断、交换两个内容合法的块（同词与跨词）、单字节篡改、
随机数据与原始集合运算交叉核对、seek 跳转的解码块数统计。
