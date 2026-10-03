# Physical Property Enforcement: Sort / Order (TypeScript)

自底向上跟踪与强制数据的物理排序特性（ordering property）的查询优化器组件：

- 每个物理算子声明自己**提供**的输出有序性（`providedOrdering`）以及对每个输入**要求**的有序性（`requiredChildOrderings`）。
- 优化器自底向上递归：先按父算子的要求优化子节点，再检查子节点输出是否满足要求；
  若底层算子（如 `IndexScan`）已满足所需顺序，则直接复用，**果断省去 Sort 算子**；
  否则插入 `Sort` enforcer 节点。
- `GroupBy`（流式聚合）与 `MergeJoin` 要求输入按分组/连接键有序；顶层 `ORDER BY`
  作为根节点的 required property 传入。

## 结构

- `src/ordering.ts` — 排序属性（Ordering）与前缀满足判定 `satisfies`
- `src/operators.ts` — 物理算子：`TableScan` / `IndexScan` / `Sort` / `GroupBy` / `MergeJoin`
- `src/optimizer.ts` — 自底向上的 property enforcement（`enforceOrdering` / `optimize`）
- `examples/demo.ts` — 运行样例
- `test/optimizer.test.ts` — Node.js 内置测试（`node:test` + `node:assert`）

## 运行

需要 Node.js >= 22.6（使用内置的 `--experimental-strip-types` 直接运行 TypeScript，
无需编译步骤；Node >= 23.6 可去掉该 flag）。

```bash
npm test    # 运行测试
npm run demo  # 运行样例
```

## 验收场景

查询 `SELECT * FROM users u ORDER BY u.id`，底层为 `IndexScan(u.id)`：
优化器识别数据已有序，直接省去物理 Sort 算子，避免昂贵的外部排序：

```
=== Acceptance: index already provides the required order ===
SQL: SELECT * FROM users u ORDER BY u.id
Physical plan:
IndexScan(users USING pk_users_id -> u.id ASC)
Stats: sortsInserted=0, sortsAvoided=1
```

对照组（底层为无序 `TableScan`）则插入 Sort enforcer：

```
Sort(ORDER BY u.id ASC)
  TableScan(users)
Stats: sortsInserted=1, sortsAvoided=0
```

## 测试结果

`npm test` 输出（10 个用例全部通过）：

```
# tests 10
# pass 10
# fail 0
```

覆盖：验收场景（IndexScan 省 Sort）、无序扫描插入 Sort、方向不匹配（ASC vs DESC）
仍插入 Sort、复合索引前缀满足、MergeJoin 双侧索引免 Sort、MergeJoin 无序双侧各插
一个 Sort、GroupBy 无序插入 Sort、GroupBy 复用索引序、GroupBy 输出序满足下游 ORDER BY。
