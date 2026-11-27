# SQL MERGE for TypeScript

一个可导入的 TypeScript 库，用于按 SQL 标准语义执行
`MERGE INTO target USING source ON join_pred`，并在语句内校验唯一索引。

MERGE 不是普通 UPSERT：本库显式实现以下硬约束。

- **分支短路**：`WHEN MATCHED` / `WHEN NOT MATCHED [BY TARGET]` /
  `WHEN NOT MATCHED BY SOURCE` 分支严格按声明顺序测试，第一条条件为
  `true` 的分支执行后立即停止，绝不落入后续分支。
- **单行基数冲突**：多个源行匹配同一个目标行时，若该目标行已被之前的匹配
  动作实际 `UPDATE`/`DELETE`，之后的 `UPDATE`/`DELETE` 立即抛出
  `CARDINALITY_VIOLATION`。之前的 `DO NOTHING` 或所有条件为假不算修改。
- **快照可见性**：本语句 `WHEN NOT MATCHED` 插入的目标行对同一语句的后续
  源行不可见，因此不会被再次匹配。
- **唯一约束时机**：`IMMEDIATE` 在每次写入后检查；`DEFERRED` 只在语句结束
  时检查，因此支持单条 MERGE 内原子交换唯一键。两种模式都支持
  `NULLS DISTINCT` 与 `NULLS NOT DISTINCT`。

失败语句不会修改调用方传入的 `target`/`source` 数组或行对象，等价于整条
MERGE 原子回滚。

## 使用

```ts
import {
  executeMerge,
  update,
  deleteRow,
  insert,
  doNothing,
} from "./dist/src/index.js";

const result = executeMerge({
  target: [
    { id: 1, name: "old", active: true },
    { id: 2, name: "remove", active: true },
  ],
  source: [
    { id: 1, name: "new", deleted: false },
    { id: 3, name: "insert", deleted: false },
  ],

  on: (target, source) => target.id === source.id,

  whenMatched: [
    {
      condition: (_target, source) => source.deleted === true,
      action: deleteRow(),
    },
    {
      action: update({
        name: (_target, source) => source.name,
        active: true,
      }),
    },
  ],

  whenNotMatched: [
    {
      action: insert((source) => ({
        id: source.id,
        name: source.name,
        active: true,
      })),
    },
  ],

  whenNotMatchedBySource: [
    {
      condition: (target) => target.active === false,
      action: doNothing(),
    },
    {
      action: deleteRow(),
    },
  ],
});

console.log(result.rows);
// [
//   { id: 1, name: "new", active: true },
//   { id: 3, name: "insert", active: true },
// ]
```

`executeMerge` 还有别名 `mergeInto`、`merge`、`runMerge`、`mergeRows`
（后者只返回行数组）。默认导出也是 `executeMerge`。

## 语句结构

```ts
interface MergeStatement<Row extends object> {
  target: readonly Row[] | { rows: readonly Row[] };
  source: readonly Row[] | { rows: readonly Row[] };
  on: (target: Row, source: Row, context: MergeContext<Row>) => boolean;

  whenMatched?: MergeBranch<Row> | readonly MergeBranch<Row>[];
  whenNotMatchedByTarget?: MergeBranch<Row> | readonly MergeBranch<Row>[];
  whenNotMatched?: MergeBranch<Row> | readonly MergeBranch<Row>[]; // 别名
  whenNotMatchedBySource?: MergeBranch<Row> | readonly MergeBranch<Row>[];

  uniqueIndexes?: readonly UniqueIndexInput<Row>[];
  uniqueConstraints?: ...; // uniqueIndexes 的别名
  constraints?: ...;       // uniqueIndexes 的别名
}
```

分支对象：

```ts
interface MergeBranch<Row extends object> {
  condition?: boolean | ((...args: any[]) => boolean);
  action?: MergeAction<Row> | string;
  // 也可使用 then / update / delete / insert / doNothing 简写
}
```

动作可以用构造器书写，也可以直接写对象：

```ts
update({ code: (target, source) => source.code })
deleteRow()
insert((source) => ({ id: source.id, code: source.code }))
doNothing()

// 等价对象形式
{ type: "update", set: { code: "X" } }
{ type: "delete" }
{ type: "insert", values: (source) => ({ id: source.id }) }
{ type: "doNothing" }
```

匹配到目标行的条件与赋值函数接收 `(target, source, context)`。源行未匹配时
可使用单参数 `(source) => ...`；仅目标行未匹配时可使用 `(target) => ...`。
若希望显式接收 target 与 source，使用统一的 `(target, source, context)`
形式（未匹配的一侧为 `null`）。

## 唯一索引

```ts
const result = executeMerge({
  target: [
    { id: 1, code: "A" },
    { id: 2, code: "B" },
  ],
  source: [
    { id: 1, code: "B" },
    { id: 2, code: "A" },
  ],
  on: (target, source) => target.id === source.id,
  whenMatched: [{ action: update({ code: (_target, source) => source.code }) }],

  uniqueIndexes: [
    {
      name: "ux_code",
      columns: ["code"],
      timing: "deferred",
      nulls: "distinct",
    },
  ],
});
```

`timing` 可以是 `"immediate"`（默认）或 `"deferred"`。同一配置也可用
`deferred: true`。

`nulls` 默认为 `"distinct"`：

- `NULLS DISTINCT`：任一键组件为 `null`/`undefined` 时，该行不参与唯一性
  比较，因此可存在多行相同位置的 `NULL`。
- `NULLS NOT DISTINCT`：`NULL` 被视为相等，多行 `NULL` 冲突；也可写
  `nullsNotDistinct: true` 或 `nullsDistinct: false`。

独立校验函数：

```ts
import { validateUniqueConstraints, findUniqueViolation } from "./dist/src/index.js";

validateUniqueConstraints(rows, [
  { name: "ux_code", columns: ["code"], nulls: "not distinct" },
]);

const violation = findUniqueViolation(rows, [{ columns: ["code"] }]);
```

## 错误

错误均为可 `instanceof` 判断的 `MergeError` 子类，并带稳定的 `code`：

- `CardinalityViolationError.code === "CARDINALITY_VIOLATION"`
- `UniqueViolationError.code === "UNIQUE_VIOLATION"`
- `MergeError.code === "INVALID_MERGE_STATEMENT"`（配置错误）

基数冲突错误还提供 `targetIndex`、`sourceIndex`、`targetRow`、`sourceRow`
和 `action`；唯一冲突错误提供 `index`、`key`、`rowIndexes` 和 `rows`。

## 有状态表

```ts
import { MergeTable } from "./dist/src/index.js";

const table = new MergeTable({
  rows: [{ id: 1, code: "A" }],
  uniqueIndexes: [{ columns: ["code"], timing: "deferred" }],
});

table.merge({
  source: [{ id: 1, code: "B" }],
  on: (target, source) => target.id === source.id,
  whenMatched: [{ action: update({ code: (_target, source) => source.code }) }],
});

console.log(table.rows);
```

`MergeTable.merge()` 只在成功后替换内部行数组；失败时保持原状态。

## 开发

```bash
npm run typecheck
npm test
```

`npm test` 先用 `tsc` 构建到 `dist/`，再运行 `node:test` 验收测试。
测试覆盖分支短路、基数冲突回滚、`DO NOTHING` 不触发冲突、插入行不可见、
`DEFERRED` 唯一键交换，以及两种 NULL 唯一性模式。
