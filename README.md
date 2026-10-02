# B+ 树 Slotted-Page 二进制槽位编码器 (TypeScript)

在单个 4KB 页内实现 B+ 树叶子节点与内部节点的紧凑二进制编码，页内支持
O(log K) 二分查找。零依赖，使用 Node.js (>= 22.6) 原生 TypeScript
类型擦除与内置测试运行器。

## 页面布局

```
+------------------------------------------------------------------+
| Header (16B) | Slot Directory ---> |  free space  | <--- Records |
+------------------------------------------------------------------+
0              16                   freeStart      freeEnd      4096
```

- **页头 (16B, 小端)**：`pageType u8` | `flags u8` | `slotCount u16` |
  `freeStart u16` | `freeEnd u16` | `aux u32`（叶子：右兄弟页号；内部节点：最左子页号）| 保留 4B
- **槽位目录 (4B/项，按 Key 升序)**：`recordOffset u16` + `keyLength u16`，
  从页头之后**向页面中间增长**
- **记录区（变长，从页尾向中间增长）**：
  - 叶子节点：`[key bytes][RID: pageId u32, slotId u16]`（值 6 字节）
  - 内部节点：`[key bytes][childPageId u32]`（值 4 字节）

Key 按无符号字节序（memcmp 序）比较，槽位目录始终有序，因此页内查找为
二分查找，比较次数不超过 `ceil(log2(K))`。

## API

- `LeafPage`
  - `insert(key, rid)` — 有序插入，重复键抛 `DuplicateKeyError`，页满抛 `PageFullError`
  - `lookup(key)` — 二分查找，返回 `{ rid, slot, offset }`（槽位下标 + 页内记录偏移）
  - `delete(key)` / `entries()` / `setNextLeaf(pageId)`
- `InternalPage`
  - `insert(key, childPageId)` — 插入分隔键
  - `findChild(key)` — 路由到可能包含该键的子页（`leftmostChild` 覆盖小于首个分隔键的范围）
- 每次二分查找后可通过 `page.lastProbeCount` 读取实际比较次数

## 运行

```bash
npm test     # Node.js 内置测试运行器，12 个用例
npm run demo # 运行样例：150 个变长 Key 写入单页并全部二分定位
```

## 验收结果

- 单个 4KB 页插入 **150 个变长字符串 Key**（14–18 字节）：槽位目录
  `[16, 616)` 向中间增长，记录区 `[796, 4096)` 自页尾向中间增长，剩余
  180B 中间空隙，互不重叠
- 任意 Key 查询均为页内二分查找：实测最大 **8 次比较**（`ceil(log2 150) = 8`），
  平均 7.31 次，并精确定位槽位下标与记录偏移
- 内部节点 150 个分隔键的路由同样不超过 8 次比较

测试结果（`npm test`）：

```
✔ empty leaf page has a well-formed header
✔ acceptance: 150 variable-length string keys fit in one 4KB page
✔ slot directory stays sorted ascending under shuffled insertion
✔ every key is located in O(log K) probes with the exact slot offset
✔ lookup returns null for absent keys
✔ duplicate keys are rejected
✔ page full raises PageFullError instead of corrupting the page
✔ delete removes the slot and keeps the page searchable
✔ page survives a round-trip through its raw buffer
✔ internal node routes keys to the correct child in O(log K)
✔ internal node with 150 separators supports binary-search routing
✔ binary encoding: slot directory and records are laid out as specified
ℹ tests 12  ℹ pass 12  ℹ fail 0
```

## 文件结构

- `src/slotted-page.ts` — 核心实现：`SlottedPage` 基类 + `LeafPage` / `InternalPage`
- `src/demo.ts` — 运行样例（`npm run demo`）
- `test/slotted-page.test.ts` — Node.js 内置测试（`npm test`）
