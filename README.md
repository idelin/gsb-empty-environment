# SkipList MemTable —— 并发只读快照迭代器

基于多层随机链表的内存有序表（MemTable），TypeScript 实现，Node.js 原生运行（≥ 22.6，利用内置 TypeScript 类型擦除，无需编译）。

## 设计

- **多层随机链表**：`MAX_LEVEL = 16`，层晋升概率 `p = 0.5`，查找 / 插入期望 `O(log n)`。
- **无锁指针更新**：层间指针封装在 `AtomicRef`（`load` / `store` / `compareExchange`）中；
  插入时先挂好新节点的后继，再自底向上逐层 CAS 拼接，失败则重新定位重试 ——
  与多线程 lock-free 跳表算法结构一致（JS 单线程模型下同步代码段天然原子）。
- **MVCC 快照隔离**：每次写入分配全局单调递增序列号 `seq`；同一键的多次写入形成版本链。
  迭代器创建时记录 `snapshotSeq`，遍历期间跳过 `seq > snapshotSeq` 的节点、
  对每个键只读 `seq <= snapshotSeq` 的最新版本，因此对后续并发插入与更新完全免疫。

## API

```ts
const list = new SkipList<number, string>();
list.insert(1, 'a');                 // 插入 / 更新
list.get(1);                         // 点查（最新版本）
list.iterator();                     // 全量快照迭代器
list.range({ start: 10, end: 20 });  // 范围扫描 [10, 20)
list.range({ start: 10, end: 20, startInclusive: false, endInclusive: true }); // (10, 20]
```

迭代器实现 `IterableIterator<[K, V]>`，可直接 `for...of`，输出严格按键升序。

## 运行

```bash
npm test     # node --test（Node.js 内置测试运行器）
npm run demo # 运行 examples/demo.ts 演示
```

## 验收覆盖（tests/skiplist.test.ts）

| 验收边界 | 测试 |
| --- | --- |
| 连续随机插入 1 万个整数键值对 | `连续随机插入 1 万个整数键值对，全量迭代严格有序且完整` |
| 任意区间迭代器输出严格单调递增 | `任意区间迭代器：边界正确且输出严格单调递增`（200 个随机区间 + 边界区间，开闭区间组合） |
| 遍历中持续写入，结果集稳定隔离 | `迭代遍历过程中持续插入新数据，迭代器输出完全稳定隔离`、`并发交错（异步任务）写入期间，多个快照迭代器各自隔离` |
| 同键更新的快照隔离 | `同一键的并发更新对快照迭代器隔离（MVCC 版本链）` |

## 测试结果

```
✔ 连续随机插入 1 万个整数键值对，全量迭代严格有序且完整
✔ 任意区间迭代器：边界正确且输出严格单调递增
✔ 迭代遍历过程中持续插入新数据，迭代器输出完全稳定隔离
✔ 并发交错（异步任务）写入期间，多个快照迭代器各自隔离
✔ 同一键的并发更新对快照迭代器隔离（MVCC 版本链）
✔ 空表与单元素边界
ℹ tests 6  ℹ pass 6  ℹ fail 0
```

## 文件结构

```
src/skiplist.ts        # 跳表 + AtomicRef + MVCC 快照迭代器
tests/skiplist.test.ts # node:test 验收测试
examples/demo.ts       # 运行样例
```
