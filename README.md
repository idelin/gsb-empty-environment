# lineage-pipeline

对 JSON 兼容树执行 **筛选 / 拆分 / 聚合 / 连接**，并保留 **输出字段 → 原始路径** 的多对多血缘映射。血缘可组合、去重、按版本查询；错误与来源均可定位到具体路径。

## 运行

需要 Node ≥ 23.6（内置 TypeScript 类型擦除；Node 22.6+ 可加 `--experimental-strip-types`）。零依赖。

```sh
npm test          # node --test test/*.test.ts
npm run sample    # node examples/sample.ts
```

## 模型

- `Evidence = { source, path }`：一条证据指向某来源数据集内的具体路径（`$[0].amount`）。
- `Tracked = { value, evidence }`：每个值携带证据集，按 `(source, path)` 去重。
- `LineageRecord = { version, stage, output, evidence }`：输出字段到证据集的多对多映射。
- 对象键无序、数组有序：路径段键用 string、下标用 number；分组/连接键用键排序的规范化序列化，数组保序。

## 语义保证

- **可组合**：算子输入输出都是 `Relation`，证据随值流动，链式阶段自动衔接；`Pipeline` 每阶段提交一个递增版本。
- **去重**：证据按 `(source, path)` 去重；血缘记录按 `(version, output, evidence-set)` 去重。
- **按版本查询**：`lineage(v)` 精确版本、`asOf(v)` 累积最新、`explain(output, v?)` 按输出字段反查。
- **聚合溯源**：度量字段证据 = 全部贡献行对应字段证据的并集。
- **过滤语义**：被过滤的行不进入下游，因而不出现在派生血缘中。
- **同值不同来源**：证据键含来源，绝不合并为同一证据。
- **错误定位**：`PathError` 携带 `path` 与 `source`，消息形如 `expected number, got "oops" (at $[1].amount in source "bad")`。

## API 一览

```ts
const p = new Pipeline();
const orders = p.source("orders", [...]);            // 注册来源（版本 1）
const kept = p.run("filter", filterRel(orders, r => ...));
const joined = p.run("join", joinRel(kept, regions, { left: "region", right: "region" }));
const agg = p.run("agg", aggregateRel(joined, { groupBy: ["region"], measures: { total: { op: "sum", field: "amount" } } }));
const groups = splitRel(kept, r => ...);             // Map<key, Relation>，保序
p.explain("$[0].total");                             // 反查贡献证据
p.lineage(4); p.asOf(3);                             // 版本查询
```

下钻嵌套路径用 `drill(tracked, ["list", 0, "v"], at)`：单证据时精确延长来源路径，派生值保留证据集。
