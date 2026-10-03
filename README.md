# B+ 树范围扫描（Range Scan）迭代器与页边界穿越 — TypeScript 版

支持高效有序范围查询的 B+ 树实现：根据起始 Key 自根向下定位叶子节点及槽位，
随后沿叶子链表向前迭代；跨越物理页边界时自动 **Pin 新页 / Unpin 旧页**，
迭代器任意时刻最多持有 1 个叶子页的 Pin，缓冲池无页面泄漏。

## 结构

- `src/buffer_pool.ts` — `DiskManager`（模拟磁盘，写页时深拷贝序列化）与
  `BufferPool`（固定容量帧、LRU 置换、只淘汰 `pinCount === 0` 的页；
  所有帧都被 Pin 时抛错，Pin 泄漏会立刻暴露）。
- `src/btree.ts` — `BPlusTree`（插入 / 分裂 / 点查）与
  `RangeScanIterator`（半开区间 `[startKey, endKey)` 的有序迭代器，
  实现 `IterableIterator`，支持 `break` 后 `close()` / `Symbol.dispose` 释放 Pin）。
- `test/range_scan.test.ts` — Node.js 内置 `node:test` 测试。
- `demo.ts` — 运行样例。

## 环境

Node.js ≥ 22.6（利用内置 TypeScript 类型擦除直接运行 `.ts`，无需编译；
Node ≥ 23.6 可省略 `--experimental-strip-types` 标志）。

## 运行

```bash
nvm use            # .nvmrc 指定 v22.14.0
npm test           # node --experimental-strip-types --test test/*.test.ts
npm run demo       # node --experimental-strip-types demo.ts
```

## 运行样例输出（npm run demo）

```
插入 10000 条记录 (key: 0..9999, value: key * 10)，叶子容量 64...
建树完成：根页 #69，缓冲池驻留 8/8 页，pinned = 0

执行 Scan(2500, 7500) ...
扫描结果：共 5000 条记录
  前 5 条: 2500, 2501, 2502, 2503, 2504
  后 5 条: 7495, 7496, 7497, 7498, 7499
  扫描期间 fetchPage 159 次（含跨页），磁盘读 158 次
  扫描结束后 BufferPool pinned = 0（0 表示无页面泄漏）

校验：5000 条 = true，严格递增不重复 = true
```

## 测试结果（npm test，Node v22.14.0）

```
ok 1 - 建树后无 Pin 泄漏，点查抽样正确
ok 2 - Scan(2500, 7500) 顺序输出 5000 条不重复记录，且无页面泄漏
ok 3 - 提前终止（break + close）同样不泄漏页面
ok 4 - 边界：起始键落在页中间 / 空区间 / 超出最大键
ok 5 - 缓冲池容量极小（2 帧）时扫描依然正确且无泄漏

# tests 5
# pass 5
# fail 0
```

（Node v23.9.0 下同样 5/5 通过。）

## 验收边界对应

- 树中 10000 条连续记录（key `0..9999`，value `key * 10`）。
- `Scan(2500, 7500)` 输出 `[2500, 7500)` 共 **5000 条**、严格递增、不重复。
- 测试使用容量仅 8 帧的缓冲池（树有 300+ 个叶子页）：扫描跨越约 157 个
  物理页边界，结束后 `pinnedCount === 0`；容量压到 2 帧仍正确，
  证明无页面泄漏、无内存溢出。
