# grouped-aggregator

按组分组的流式聚合库（TypeScript），支持以事件标识插入与撤回，并为每组维护
计数、和、最小值与前两名（top-2）。

## 设计要点

- **精确可撤回的索引，而非只增不减的摘要**：每组维护一个按 `(value, eventId)`
  排序的精确多重集合（二分插入/删除）。撤回当前最小值后能正确揭示次小值——
  这是单调摘要（如仅保存 running min）无法做到的。
- **有界索引**：索引大小始终等于存活事件数，撤回即收缩；`indexSize()` 可用于验证。
- **空组删除**：组内最后一条事件被撤回或移出后，组状态整体移除，
  `stats(group)` 返回 `undefined`。
- **原子换组**：`update(eventId, group, value)` / `moveToGroup(eventId, group)`
  先完成全部校验再一次性执行“撤回 + 插入”，失败时不留下任何中间状态。
- **重复撤回校验**：撤回不存在或已撤回的事件抛出 `UnknownEventError`；
  重复插入存活事件抛出 `DuplicateEventError`。
- **并列排名**：值相同按 `eventId` 字典序确定性排序，top-2 结果稳定可复现。

## API

```ts
const agg = new GroupedAggregator();
agg.insert("e1", "group-a", 5);
agg.update("e1", "group-b", 3);   // 原子换组并改值
agg.moveToGroup("e1", "group-a"); // 原子换组，保留值
agg.retract("e1");                // 再次调用会抛 UnknownEventError
agg.stats("group-a");             // { group, count, sum, min, top2 } | undefined
```

## 测试

```sh
bun test
```
