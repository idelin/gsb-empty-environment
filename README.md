# skiplist-memtable

基于多层随机链表的内存有序表（MemTable），TypeScript 实现，Node.js 原生运行（≥ 22.6，推荐 ≥ 23.6 免 flag 运行 `.ts`）。

## 特性

- **多层随机跳表**：`MAX_LEVEL = 16`，晋升概率 `p = 0.5`，插入 / 查询期望 `O(log n)`。
- **并发有序插入**：Node 单线程事件循环下，每次插入的前驱搜索与指针拼接之间不含 `await`，层级索引的指针切换（pointer swing）对事件循环而言是原子的无锁更新；异步写入者与读取者只会在明确的让出点交错，永远不会观察到链接到一半的节点。
- **MVCC 快照隔离迭代器**：每次写入分配单调递增的序列号 `seq`；同键覆盖写以版本链形式追加，节点在表生命周期内不物理删除。迭代器在创建时冻结 `snapshotSeq`，遍历不可变的 0 层链时只返回 `seq <= snapshotSeq` 的最新版本——后续并发插入 / 覆盖对迭代器完全不可见，结果集稳定隔离。
- **范围扫描**：`iterator({ gte, lte })` 利用层级索引定位下界，输出严格单调递增。

## API

```ts
import { SkipList } from "./src/skiplist.ts";

const list = new SkipList<number, string>();
list.insert(10, "a");            // -> 返回本次写入的 seq
list.get(10);                    // -> "a"
list.size;                       // 唯一键数量
list.currentSeq;                 // 当前快照点

for (const { key, value } of list.iterator({ gte: 5, lte: 20 })) { ... }
list.entries({ snapshotSeq: 42 }); // 读取历史快照
```

## 运行

```bash
npm test    # node --test（内置测试运行器，5 个用例）
npm run demo
```

## 测试覆盖（验收边界）

1. **1 万随机插入**：全表扫描键数与参考 `Map` 一致、严格单调递增、值为最后一次写入。
2. **任意区间迭代器**：100 个随机区间（含空区间）逐一与全扫描暴力过滤结果比对，验证边界与单调性。
3. **迭代中并发写入**：迭代器创建后，4 个异步写入者交错插入 / 覆盖 1 万个键（含扫描区间内的键），迭代器输出与创建时快照逐元素相等；新迭代器则能看到新数据。
4. **历史快照**：通过显式 `snapshotSeq` 回读任意历史轮次的状态。
5. **快照下覆盖写**：旧迭代器保留旧值，新迭代器看到新值。
