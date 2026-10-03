# B+ 树级联收缩与物理合并（TypeScript）

在页式存储层上实现 B+ 树删除路径：**兄弟借调优先 → 兄弟均无法借调时物理合并 →
父节点删除索引项 → 父节点下溢则递归向上合并 → 根节点收缩**。所有被清空的页面
立即归还至空闲页链表（Free Page List）并被后续分配复用。

## 结构

- `src/pageManager.ts` — 页式存储模拟：分配 / 回收 / 空闲页链表复用
- `src/bplustree.ts` — B+ 树：批量加载、查找、删除（借调 + 级联物理合并 + 根收缩）
- `test/merge.test.ts` — Node.js 内置测试（`node:test` + `node:assert`）
- `examples/demo.ts` — 运行样例：500 条记录逐删至 3 条，观察树高与页回收

参数：`MAX_KEYS = 4`，`MIN_KEYS = 2`。500 条记录批量加载为满树：
125 叶子页 + 25 + 5 + 1 内部页 = 156 页，树高 4。

## 运行

```bash
npm test    # node --test test/merge.test.ts
npm run demo
```

## 运行样例（`npm run demo`）

```
bulk-loaded full tree  height=4  records=500  pages(in-use/free/total)=156/0/156  merges=0 borrows=0 rootShrinks=0
after 401 deletes      height=3  records= 99  pages(in-use/free/total)=31/125/156  merges=124 borrows=248 rootShrinks=1
after 481 deletes      height=2  records= 19  pages(in-use/free/total)=6/150/156  merges=148 borrows=296 rootShrinks=2
after 497 deletes      height=1  records=  3  pages(in-use/free/total)=1/155/156  merges=152 borrows=304 rootShrinks=3
final (497 deletes)    height=1  records=  3  pages(in-use/free/total)=1/155/156  merges=152 borrows=304 rootShrinks=3
remaining records: [498, 499, 500]
```

## 测试结果（`npm test`）

```
✔ bulk-loaded full tree: height 4, fully packed pages, empty free list
✔ acceptance: delete 500 -> 3 records, height shrinks 4 -> 1, pages recycled
✔ free page list reuses recycled page ids
✔ cascading merge is order-independent (seeded random deletion)
✔ deleting a missing key is a no-op
✔ max keys constant sanity
ℹ tests 6  ℹ pass 6  ℹ fail 0
```

## 验收边界覆盖

- 500 节点满树（高 4）逐条删除至 3 条，树高按 4 → 3 → 2 → 1 单调收缩，根收缩 3 次
- 每次删除后校验全部结构不变量：最小填充、分隔键一致性、父子指针、叶层同深、键有序
- 结束时仅剩 1 个在用页（根叶页），155 个空页全部回收至 Free Page List，无泄漏
- 空闲页链表复用：回收后的页 id 被后续分配弹出复用，存储不增长
- 删除顺序无关性：种子化随机删除顺序下同样收敛到高 1、1 页在用
