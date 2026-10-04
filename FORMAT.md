# CIDX 紧凑索引段格式（版本 1）

所有整数为小端无符号。`u32` 为 4 字节，`u16` 为 2 字节。文档 ID 合法范围
`[0, 4294967295]`（u32）。文件字节布局：

```
+----------------------------+
| Header        (28 bytes)   |
+----------------------------+
| Dictionary  (dictLength)   |
+----------------------------+
| Postings  (postingsLength) |
+----------------------------+
| Digest             (4 B)   |  CRC-32(IEEE) of all preceding bytes
+----------------------------+
```

## Header（28 字节）

| 偏移 | 字段             | 类型 | 说明                          |
|-----:|------------------|------|-------------------------------|
| 0    | magic            | u32  | `"CIDX"` = 0x43494458         |
| 4    | version          | u32  | 必须为 1                      |
| 8    | termCount        | u32  | 词典条目数                    |
| 12   | dictOffset       | u32  | 词典起始绝对偏移（= 28）      |
| 16   | dictLength       | u32  | 词典字节数                    |
| 20   | postingsOffset   | u32  | posting 区起始绝对偏移        |
| 24   | postingsLength   | u32  | posting 区字节数              |

## Dictionary

`termCount` 个条目，词项按 UTF-8 字节序严格递增。每个条目：

| 字段            | 类型      | 说明                                        |
|-----------------|-----------|---------------------------------------------|
| termLen         | u16       | 词项 UTF-8 字节数（可为 0，即空词项）       |
| term            | bytes     | 词项 UTF-8 字节                             |
| postingsOffset  | u32       | 该词 posting 跨度相对 posting 区起点偏移    |
| postingsLength  | u32       | 该词 posting 跨度字节数                     |
| totalCount      | u32       | 文档 ID 总数（各块 count 之和）             |
| blockCount      | u32       | 块数（= 跳转表条目数）                      |
| jump[blockCount]| 12 B each | 块跳转表：`{relBlockOffset u32, maxDocID u32, count u32}` |

跳转表按块顺序排列，`maxDocID` 严格递增；`relBlockOffset` 相对该词
posting 跨度起点。查询 seek 时用 `maxDocID` 二分定位目标块，无需解码
之前的所有块。

## Postings 区

每个词的 posting 跨度由若干块顺序拼接而成。块头（24 字节）：

| 偏移 | 字段          | 类型 | 说明                                       |
|-----:|---------------|------|--------------------------------------------|
| 0    | magic         | u32  | `"BLK0"` = 0x424C4B30                      |
| 4    | termHash      | u32  | 词项 UTF-8 字节的 FNV-1a，把块绑定到词项   |
| 8    | firstDocID    | u32  | 块内最小文档 ID                            |
| 12   | lastDocID     | u32  | 块内最大文档 ID（= 跳转表 maxDocID）       |
| 16   | count         | u32  | 块内文档 ID 条数                           |
| 20   | payloadLength | u32  | 差分负载字节数                             |

负载为 `count` 个 u32 varint（LEB128，最多 5 字节，第 5 字节 ≤ 0x0F）：
第一个是该块的第一个文档 ID（绝对值，可为 0），其余为与前一个 ID 的
差值，必须 ≥ 1（严格递增）。解码出的 ID 不得超过 4294967295。

## Digest

最后 4 字节为 CRC-32（IEEE 802.3，多项式 0xEDB88320），覆盖此前全部
字节。任何截断或字节级修改都会使摘要不匹配。

## 校验器检查项

- 头部魔数、版本；词典/posting 区域边界与相互重叠
- 词典：条目边界、词项严格递增、跳转表边界
- 词项 posting 跨度：越界与词项间重叠
- 跳转表：偏移递增且与实际块位置一致、`maxDocID` 递增、count 之和
  等于 totalCount
- 块：魔数、termHash 与词典词项绑定、first/lastDocID 与跳转表一致、
  块间 ID 严格递增（交换两个内容合法的块会被检出）、负载恰好解码出
  count 个差分、差分 ≥ 1（非递增 ID 检出）、累积 ID 不溢出 u32
- 尾部 CRC-32 摘要

每个错误都携带文件字节区间 `[offset, offset+length)` 与所属词项。
只有校验全部通过的段才能用于查询。
