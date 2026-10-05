# 紧凑索引段文件格式（CIDX v1）

所有整数为**小端序**。文档ID为无符号32位整数，合法范围 `[0, 2^32-1]`。
文件由四段顺序组成：`Header | Dictionary | Postings | Summary`。

## 1. Header（固定 58 字节）

| 偏移 | 长度 | 字段 | 说明 |
|---|---|---|---|
| 0 | 4 | magic | ASCII `CIDX` |
| 4 | 1 | version | 固定 `1` |
| 5 | 1 | flags | 保留，固定 `0` |
| 6 | 4 | termCount | u32，词项数量 |
| 10 | 8 | dictOffset | u64，词典段绝对偏移 |
| 18 | 8 | dictLength | u64，词典段字节数 |
| 26 | 8 | postingsOffset | u64，posting 段绝对偏移 |
| 34 | 8 | postingsLength | u64，posting 段字节数 |
| 42 | 8 | summaryOffset | u64，摘要段绝对偏移 |
| 50 | 8 | summaryLength | u64，摘要段字节数，固定 `36` |

## 2. Dictionary 段

`termCount` 个词项记录顺序排列，词项按 UTF-8 字节序升序。每个记录：

| 长度 | 字段 | 说明 |
|---|---|---|
| 2 | termLen | u16，词项 UTF-8 字节数（可为 0，即空词项） |
| termLen | term | 词项 UTF-8 字节 |
| 4 | blockCount | u32，该词项的 posting 块数（可为 0） |
| blockCount × 20 | skip table | 每块一个跳转表项，见下 |

跳转表项（20 字节，按块内文档范围升序）：

| 长度 | 字段 | 说明 |
|---|---|---|
| 4 | lastDocId | u32，本块最大文档ID（seek 二分依据） |
| 4 | count | u32，本块 posting 条数 |
| 8 | offset | u64，块在文件中的绝对偏移 |
| 4 | length | u32，块字节数 |

## 3. Postings 段（块序列）

每个块把**词项、文档范围、条数**绑定在一起：

| 长度 | 字段 | 说明 |
|---|---|---|
| 2 | termLen | u16 |
| termLen | term | 词项字节，必须与词典中指向本块的词项一致 |
| 4 | firstDocId | u32，本块最小文档ID |
| 4 | lastDocId | u32，本块最大文档ID |
| 4 | count | u32，posting 条数，≥ 1 |
| 4 | payloadCrc | u32，载荷字节的 CRC-32(IEEE) |
| 变长 | payload | `count` 个 uvarint（LEB128，最多 5 字节） |

载荷编码：第一个值是**绝对文档ID**，其余是**严格正的差分**（≥1）。
解码时累积值不得超过 `2^32-1`，否则视为整数溢出。载荷必须恰好消费到块尾。

## 4. Summary 段（固定 36 字节）

| 偏移 | 长度 | 字段 | 说明 |
|---|---|---|---|
| 0 | 4 | magic | ASCII `CSUM` |
| 4 | 4 | termCount | u32，须与 Header 一致 |
| 8 | 4 | totalBlocks | u32，须与词典中块总数一致 |
| 12 | 8 | fileLength | u64，文件总字节数（截断/追加检测） |
| 20 | 4 | headerCrc | u32，Header 段 `[0,58)` 的 CRC-32 |
| 24 | 4 | dictCrc | u32，词典段的 CRC-32 |
| 28 | 4 | postingsCrc | u32，Postings 段的 CRC-32（块交换/重排检测） |
| 32 | 4 | summaryCrc | u32，摘要前 32 字节的 CRC-32 |

## 校验规则（validator 实施）

1. 结构：magic/version；三个段在文件范围内、互不重叠、不越出 Header 之前；
   每个词典块偏移落在 Postings 段内且块间不重叠。
2. 摘要：`fileLength` 等于实际文件大小；`termCount`/`totalBlocks` 与 Header、词典一致；
   四个 CRC 全部匹配。
3. 块内容：块内词项与词典指向的词项一致（防块交换）；块头 `lastDocId`/`count`
   与跳转表一致；载荷 CRC 匹配；varint 不越界（≤32 位）；文档ID严格递增；
   累积值 ≤ `2^32-1`；载荷恰好解码出 `count` 个值且无剩余字节。

校验报告的每个问题都携带文件字节区间 `[byteStart, byteEnd)` 与相关词项。
**只有校验全部通过的段才允许打开查询**（`SegmentReader.open` 先校验，失败即抛错）。

## 查询语义

- `get(term)`：解码该词项全部块，返回完整递增 posting 列表。
- `cursor.seek(id)`：利用跳转表二分定位第一个 `lastDocId ≥ id` 的块，
  **只解码目标块**，块内再二分；绝不回退、绝不解码被跳过的块。
- `intersect(terms)`：多游标归并 + seek 对齐，实现多词 AND。
