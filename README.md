# B+ 树范围扫描迭代器与页边界穿越（TypeScript）

纯 TypeScript 实现的 B+ 树，支持高效有序范围查询：
根据起始 Key 定位叶子节点及槽位，沿叶子链表向前迭代；
跨越物理页边界时自动 Pin 新页、Unpin 旧页，缓冲池无页面泄漏。

## 环境要求

- Node.js >= 23.6（原生 TypeScript 类型擦除运行，无需编译、零依赖）
- 测试使用 Node 内置 `node:test` 与 `assert`

## 运行

```bash
npm test        # 等价于 node --test，运行全部内置测试
npm run demo    # 等价于 node demo.ts，运行验收样例
```

## 结构

- `src/page.ts` — 页与节点数据布局（叶子页含 `next` 链表指针）
- `src/buffer_pool.ts` — 固定容量缓冲池：Pin/Unpin、LRU 置换、
  全池被 Pin 时抛错（用于暴露 Pin 泄漏）
- `src/bplustree.ts` — B+ 树（插入/分裂/点查）与
  `RangeScanIterator`（`scan(startKey, endKey)`，区间 `[start, end)`）
- `test/scan.test.ts` — Node 内置测试
- `demo.ts` — 验收运行样例

## 迭代器设计要点

- `scan()` 通过 `findLeaf(startKey)` 自根下降定位叶子页（保持 Pin），
  二分查找起始槽位。
- 槽位越界时沿叶子 `next` 链表前进：`fetchPage` Pin 新页后立即
  `unpinPage` 旧页，任意时刻最多持有 2 个 Pin。
- 实现 `IterableIterator` 协议；`for...of` 提前 `break` 时通过
  `return()` 释放持有的 Pin。

## 验收结果（10000 条记录，Scan(2500, 7500)，缓冲池仅 8 帧）

```
扫描结果:
  输出记录数     : 5000 (期望 5000)
  首条 / 末条 Key: 2500 / 7499
  扫描期间取页   : 155 次
  扫描期间置换   : 154 次 (跨页证据)
  结束后残留 Pin : 0 (0 = 无页面泄漏)
  页框占用       : 8/8 (未溢出)
```

测试结果：7 个用例全部通过（顺序性、无重复、跨页边界、空范围、
提前 break 释放 Pin、乱序插入后扫描有序、无 Pin 泄漏）。
