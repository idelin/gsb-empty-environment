# Overflow Page B+Tree (TypeScript)

动态变长键溢出页（Overflow Page）链式挂接器：当 Key 或 Value 超过单页内联阈值时，
主体数据被截断写入独立的溢出页链，叶子节点只保留**前缀 + 溢出页链表头指针**，
查询时透明拼合还原完整数据。

## 设计

- **页大小** `PAGE_SIZE = 4096`，页 id 从 1 开始，`0` 作为空指针/链尾哨兵。
- **内联阈值** `maxInline = PAGE_SIZE / 4 = 1024` 字节；超过即走溢出页。
- **叶子条目** 序列化格式：`flag(1) | totalLen(4) | prefixLen(2) | prefix | [overflowHead(4)]`，
  溢出时默认保留 128 字节前缀在叶子页内。
- **溢出页** 布局：`tag(1) | nextPageId(4) | chunkLen(4) | chunk...`，
  每页净载荷 `4096 - 9 = 4087` 字节，`nextPageId = 0` 表示链尾。
- Key 与 Value 共用同一套 `DataRef` 编码，二者超限都会溢出；B+ 树比较时按需还原完整键。
- 更新溢出值时释放旧链，页进入空闲链表复用。

## 文件结构

- `src/pager.ts` — 页管理器（分配/读/写/回收，空闲页复用）
- `src/overflow.ts` — 溢出页链的写入、遍历、拼合、释放
- `src/refs.ts` — `DataRef` 内联/溢出引用编码与透明还原
- `src/btree.ts` — B+ 树（插入、点查、叶子/内部节点分裂）
- `test/` — Node.js 内置 `node:test` 测试
- `examples/demo.ts` — 64KB 长文本记录的运行样例

## 运行

需要 Node.js ≥ 23.6（原生 TypeScript 类型擦除，无需编译步骤）：

```sh
npm test     # node --test test/overflow.test.ts test/btree.test.ts
npm run demo # node examples/demo.ts
```

## 验收样例输出（64KB 长文本单行记录）

```
=== B+ tree with chained overflow pages ===
stats: { pageCount: 18, rootPageId: 1, maxInline: 1024, prefixLen: 128 }

--- leaf entry for "user:1:bio" ---
value stored inline : false
value total length  : 65536 bytes
prefix kept in leaf : 128 bytes
overflow page chain : 2 -> 3 -> 4 -> ... -> 18 -> 0 (end)
overflow page count : 17

--- transparent read ---
restored length     : 65536 bytes
round-trip identical: true
```

## 测试结果

```
✔ small key/value stays inline, no overflow pages allocated
✔ payload exactly at threshold stays inline, threshold+1 overflows
✔ 64KB long text: leaf keeps prefix + overflow chain, read reassembles transparently
✔ overflow chain pages are well-formed and byte-exact
✔ oversized keys also go through overflow pages and stay ordered
✔ updating an overflowed value frees its old chain
✔ many records with mixed sizes survive leaf/internal splits
✔ random-order inserts keep every record retrievable
✔ duplicate insert replaces the value
ℹ pass 9  ℹ fail 0
```
