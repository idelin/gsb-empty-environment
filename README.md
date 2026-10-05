# grouped-aggregator

按组分组的精确聚合库（TypeScript 实现），支持事件插入与撤回。

## 功能

- **按事件标识插入/撤回**：`insert({id, group, value})` / `retract(id)`。
- **组级聚合**：计数、和、最小值、前两名（`getGroupStats(group)`）。
- **并列排名**：前两名按 dense rank 取值——取最大的两个不同值，并列事件共享名次，
  `top` 中每个名次携带全部并列事件 id。
- **原子换组**：`update(id, {group?, value?})` / `moveToGroup(id, group)`。
  所有校验（目标组容量、组数上限、值合法性）在任何变更之前完成，
  失败时索引保持原状，绝不产生"已移出但未移入"的中间态。
- **有界索引**：`maxGroups` 与 `maxEventsPerGroup` 两个上界，
  越界抛出带 `code` 的 `AggregatorError`；组被清空后立即删除，槽位可复用。
- **重复撤回校验**：撤回不存在（含已撤回）的事件抛 `UNKNOWN_EVENT_ID`；
  重复插入同一 id 抛 `DUPLICATE_EVENT_ID`。

## 为什么不用只增不减的摘要

只增不减（append-only/monotonic）的摘要——如 HyperLogLog、最大堆式 top-k 草图——
只能吸收新增贡献，无法表达"撤销"。一旦允许撤回，最小值和前两名必须能从
剩余集合中重新计算。因此本库为每组维护一个**可删除的精确多重集合**
（`value -> Set<id>` 加一条降序排列的不同值数组，二分插入），
撤回时精确移除贡献，min / top-2 永远反映当前存活事件，而非历史峰值。

## API

```ts
const agg = new GroupedAggregator({ maxGroups: 1000, maxEventsPerGroup: 10000 });
agg.insert({ id: 'e1', group: 'cn', value: 9.5 });
agg.update('e1', { group: 'us', value: 10 }); // 原子换组 + 改值
agg.retract('e1');
agg.getGroupStats('us');
// => { group, count, sum, min, top: [{ value, ids: [...] }, ...] } | undefined
```

## 开发

```sh
npm test    # node test/grouped-aggregator.test.mjs（无需依赖）
npm run build  # 需要 typescript（devDependency），输出到 dist/
```

`dist/grouped-aggregator.js` 是 `src/grouped-aggregator.ts` 的等价编译产物，
测试直接运行该产物。
