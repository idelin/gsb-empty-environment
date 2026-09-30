# sstdict — 按块前缀压缩的有序字符串词典（Go 标准库）

不可变段（segment）+ 块级公共前缀压缩的有序字符串索引，支持随机取词、
字节前缀区间查询、批量更新的段合并与旧快照。仅使用 Go 标准库。

## 编码布局（真实字节布局，小端；varint 为 uvarint）

```
Header（28 字节定长）
  magic     [4]byte  "SST1"
  blockSize uint32   每块最大词条数
  numBlocks uint32
  numWords  uint64
  indexOff  uint32   索引区绝对偏移
  indexLen  uint32   索引区字节数

Data 区（紧跟 header）：块序列
  Block:
    payloadLen uint32
    crc32      uint32   对 payload 的 IEEE 校验
    payload:
      uvarint entryCount                （必须 <= blockSize，分配前校验）
      entry[0]:   uvarint wordLen | 完整词字节     （重启点：整词存储）
      entry[i>0]: uvarint prefixLen | uvarint suffixLen | 后缀字节

Index 区（稀疏索引，每块一条）
  uvarint len(firstWord) | firstWord | uvarint blockOffset（相对 Data 区）
```

占用字节示例（测试 `TestExactByteLayout` 逐字节验证）：词 `["", "a", "ab",
"abc"]`、blockSize=2 时，header=28B、data=28B、index=6B，共 62B。
前缀密集数据（2 万条 URL）实测总占用约为朴素「4 字节长度 + 原文」表的 0.12–0.17 倍。

## 设计要点

- **块自包含**：每块首词整存（重启点），块内条目的 prefixLen 只引用本块前一个词，
  任意块可独立定位与解码，绝不引用前块内容。
- **损坏不静默错解**：每块 CRC32 校验；payload 内所有长度字段（entryCount、
  wordLen、prefixLen、suffixLen）在任何内存分配**之前**与剩余字节数/前词长度比对，
  越界即返回 `ErrCorrupt`。
- **随机取词**：除末块外每块恰有 blockSize 个词，块号 = k / blockSize 直接定位，
  单次 `Get` 最多解码 blockSize 条（`Stats.MaxDecodedPerRead` 统计）。
- **前缀区间查询**：稀疏索引二分定位起始块，按字节序扫描；前缀按字节匹配，
  返回的永远是完整存储词（UTF-8 输出完整）。
- **批量更新与合并**：每次 `AddBatch` 生成一个不可变新段；`Merge` 将所有活跃段
  k-way 归并为一个新段并原子替换。`Snapshot` 固定段列表，旧快照在合并后依然可读。
- **共享段回收条件**：段被合并取代（不再活跃）**且**无任何打开的快照引用
  （引用计数归零）时才计入可回收（`StoreStats.ReclaimedSegments/ReclaimedBytes`）。
- **重复词策略**：同一批次内重复词直接报错（`ErrDuplicate`），输入必须严格递增；
  跨批次重复词在合并/查询时按「新批次优先」折叠为一条（词集语义下结果相同）。
- **空字符串**可作为首项；`PrefixRange("")` 返回全部词。

## 运行

```sh
go test -v ./...   # 测试（含与独立 []string 的对照、损坏注入、字节布局断言）
go run ./cmd/demo  # 运行样例：批量更新、快照、合并、统计与布局输出
```
