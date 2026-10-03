# Overflow Page Chain（动态变长键溢出页链式挂接器, TypeScript）

B+ 树存储引擎原型：当 Key 或 Value 超过单页内联阈值时，字段主体被截断写入独立的
**溢出页（Overflow Page）链表**，树页面只保留 **前缀 + 链表头指针**；查询时透明拼合还原完整数据。

## 设计

- 页大小固定 `4096` 字节，页类型：`Internal(1) / Leaf(2) / Overflow(3)`（`src/pager.ts`）。
- 溢出页布局：`[type:u8][nextPageId:u32le][chunkLen:u32le][chunk...]`，
  每页净载荷 `4087` 字节，`nextPageId` 串成单向链，`0xFFFFFFFF` 结尾（`src/overflow.ts`）。
- 字段编码（Key/Value 共用，`src/field.ts`）：
  - 内联（≤ 1024B）：`[flag=0][length][data]`
  - 溢出（> 1024B）：`[flag=1][totalLength][prefixLength][prefix:64B][firstOverflowPageId]`
  - 前缀之后的 remainder 写入溢出链；读取时 `prefix + chain` 透明拼合。
- B+ 树（`src/btree.ts`）：叶子页存 `(keyField, valueField)` 序列，内部页存分隔键；
  页满分裂并向上传播分隔键。已编码字段字节缓存在内存条目上，重写页面不会重复分配溢出链。

## 运行环境

Node.js ≥ 22.18（原生 TypeScript type-stripping，无需编译、零依赖）。

## 运行样例

```bash
node examples/demo.ts
```

输出（本机 Node v25.5.0 实测）：

```
page size                 : 4096 bytes
stored value size         : 88260 bytes (64KB)
leaf keeps inline prefix  : 64 bytes
leaf prefix preview       : "The quick brown fox jumps over the lazy dog. 溢" ...
overflow chain head page  : 1
overflow chain page ids   : [1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 20, 21, 22]
overflow chain length     : 22 pages
reassembled value size    : 88260 bytes
round-trip identical      : true
small row stays inline    : a small inline value
prefix bytes per leaf rec : 64
stats                     : { pageCount: 23, overflowPageCount: 22 }
```

## 测试（Node.js 内置 `node:test`，无第三方依赖）

```bash
npm test        # 等价于 node --test 'test/*.test.ts'
```

实测结果：

```
✔ overflow chain stores a 64KB payload across linked pages
✔ small values stay inline in the leaf page
✔ 64KB record: leaf keeps prefix + overflow chain, get() reassembles transparently
✔ oversized keys also spill to overflow pages
✔ many records with mixed sizes survive leaf/internal splits
✔ re-inserting the same key replaces the value
✔ page size sanity: inline threshold and prefix fit within one page
ℹ tests 7  ℹ pass 7  ℹ fail 0
```

验收边界覆盖：向树中存入一条含 64KB 长文本的单行记录 → 叶子节点仅存 64 字节前缀
与 OverflowPageID 链表（22 页）→ `get()` 透明拼合还原出与原文逐字节一致的数据。

## 目录结构

```
src/pager.ts     定长页管理器（页 ID 寻址）
src/overflow.ts  溢出页链式挂接器（写链 / 读链 / 遍历链）
src/field.ts     变长字段编码（内联 vs 前缀+溢出链指针）
src/btree.ts     B+ 树（叶子/内部页、分裂、透明拼合查询）
test/overflow.test.ts  node:test 内置测试
examples/demo.ts       64KB 长文本记录运行样例
```
