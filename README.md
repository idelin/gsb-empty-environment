# B+ 树 Slotted-Page 二进制编码器 (TypeScript)

在单个 4 KiB 页内实现 B+ 树叶子节点与内部节点的紧凑二进制编码，
槽位目录（slot directory）从页头向页中间增长，单元格（cell）从页尾向中间增长，
键按升序排列，支持页内二分查找（O(log K)）。

## 页面布局

```
0                HEADER_SIZE(12)              freeStart       freeEnd      4096
+-----------------+---------------------------+--------------+-------------+
| page header     | slot directory (grows ->) |  free space  | cells (<- ) |
+-----------------+---------------------------+--------------+-------------+
```

- 页头 (12 B)：`u8 pageType` | `u8 flags` | `u16 slotCount` | `u16 freeStart` | `u16 freeEnd` | `u32 reserved`
- 槽位 (4 B)：`u16 cellOffset` | `u16 cellLength`，按 key 升序
- 叶子 cell：`u16 keyLen` | `key bytes` | `u32 ridPageId` | `u32 ridSlotId`
- 内部 cell：`u16 keyLen` | `key bytes` | `u32 childPageId`

## 运行

要求 Node.js >= 23.6（原生 TypeScript 类型擦除，无需编译）。

```sh
npm test     # node --test（Node 内置测试运行器）
npm run demo # 运行 examples/demo.ts
```

## 样例输出

```
leaf page: 150 slots, 48 bytes free of 4096
first 3 keys in slot order: key-0000, key-0001x, key-0002xx
find("key-0117xxxxxxx") -> slot #117, cell offset 3337, 7 comparisons (log2(150) ~ 7.2)
RID at that slot: {"pageId":7032,"slotId":32}
internal page separators: key-0000 | key-0050 | key-0100
find("key-0050") -> child page 11
```

## 测试结果

```
✔ leaf page holds 150 variable-length keys in a single 4KB page
✔ every key is located via binary search in O(log K) comparisons
✔ insertion returns exact cell offsets and slots grow toward the middle
✔ internal page stores key -> childPageId with binary search
✔ duplicate keys are rejected
✔ page full is reported when 4KB is exhausted
✔ page survives a serialization round-trip through its raw buffer
ℹ pass 7  ℹ fail 0
```

## 文件

- `src/slottedPage.ts` — 编码器实现（`SlottedPage` / `insertLeaf` / `insertInternal` / `find`）
- `test/slottedPage.test.ts` — Node 内置测试（含 150 个变长 key 验收与比较次数上界断言）
- `examples/demo.ts` — 运行样例
