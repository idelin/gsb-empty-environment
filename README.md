# General Unnesting 参考实现

零依赖 TypeScript 关系代数重写器与执行核，按 Neumann & Kemper 的 General
Unnesting 思路把相关子查询改写为
`InnerJoin / LeftOuterJoin / MarkJoin / SingleJoin`，并保留可执行计划。

核心能力：

- 从外层计划提取去重关联域 `D = DISTINCT(π_corr(T1))`，把 `D` 下推通过
  `Filter`、`Project`、普通/外连接、`Aggregate`、`Union`、`Distinct` 和
  `Limit`（`Limit` 使用按 `D` 分区的行号）。
- 相关标量 `COUNT(*)` 使用 `D LEFT OUTER JOIN 子查询输入`，聚合参数改写为
  非空探针表达式；无匹配组返回 `0`，`SUM/AVG/MIN/MAX` 返回 `NULL`。
- `EXISTS`、`NOT EXISTS`、`IN`、`NOT IN`、`= ANY`、`<> ALL` 均编译为输出
  `{TRUE, FALSE, UNKNOWN}` 的 `MarkJoin`（值为 `true | false | null`）。
- 非聚合标量子查询编译为 `SingleJoin`：0 行补 `NULL`，多行抛
  `CardinalityViolationError`。
- 提供逐行嵌套参考执行器，可和去相关计划逐行对照。

## 构建与测试

环境中的 Node 未启用 TypeScript strip，因此测试先由本地 `tsc` 编译：

```bash
npm test
```

等价的步骤：

```bash
npm run build
node --test dist/test/*.test.js
```

源码入口为 `src/index.ts`，编译产物入口为 `dist/index.js`。包内 TypeScript
源码使用 NodeNext 的 `.js` 说明符，由 `tsc` 解析并映射到同目录 `.ts` 文件。

## 快速开始

下面的查询等价于
`SELECT R.id, (SELECT COUNT(*) FROM S WHERE S.rid = R.id) FROM R`：

```ts
import {
  aggregate,
  col,
  count,
  eq,
  evaluatePlan,
  executeUnnested,
  filter,
  project,
  scalar,
  table,
  compareExecution,
} from "ps-6f01e031fe";

const context = {
  catalog: {
    R: { columns: ["id"], rows: [{ id: 1 }, { id: 2 }] },
    S: {
      columns: ["rid", "v"],
      rows: [
        { rid: 1, v: 10 },
        { rid: 1, v: 20 },
      ],
    },
  },
};

const subquery = project(
  [{ as: "cnt", expr: col("cnt") }],
  aggregate(
    filter(eq(col("S", "rid"), col("R", "id")), table("S", "S")),
    [],
    [count("cnt")],
  ),
);

const query = project(
  [
    { as: "id", expr: col("R", "id") },
    { as: "cnt", expr: scalar(subquery) },
  ],
  table("R", "R"),
);

const reference = evaluatePlan(query, context);
const optimized = executeUnnested(query, context);

// reference.rows 与 optimized.rows 逐行等价
// [{ id: 1, cnt: 2 }, { id: 2, cnt: 0 }]
const comparison = compareExecution(query, context);
```

## 表与行

表数据可以是：

```ts
{
  catalog: {
    R: { columns: ["id"], rows: [{ id: 1 }] },
    S: [{ rid: 1, v: 10 }], // 空表时建议显式给 schema
  },
  schema: {
    S: ["rid", "v"],
  },
}
```

也可以用内联的 `table(name, alias)`，或在 `TableNode` 上提供
`columns`/`rows`。扫描后列名按 SQL 别名限定为 `R.id`、`S.v`；表达式使用
`col("R", "id")`（也接受 `col("R.id")`）。无歧义的单列名可以用
`col("id")`。

## 语义实现

### 关联域下推

1. `freeColumns(query)` 找出子查询中未被本层表别名绑定的列。
2. `buildDomain` 生成 `DISTINCT(π_corr(outer))`。
3. `decorrelatePlan` 递归把 `D` 下推到子查询的叶子输入。`Project` 自动保留
   `D` 键，`Aggregate` 增加 `D` 分组键，`Union` 对两个分支分别下推，
   `Limit` 改写为 `ROW_NUMBER() OVER (PARTITION BY D ...)`。
4. 连接两侧可能出现同名 `D` 键，因此右分支键先重命名为内部键，再用
   `IS NOT DISTINCT FROM` 做空安全等值连接，最后折叠内部键。这避免普通
   `row = { ...left, ...right }` 覆盖左键造成伪匹配。
5. 根计划只保留原始输出列，所有 `__gsb_*` 内部列都会被移除。

### 空组聚合

对关联的无 `GROUP BY` 聚合，改写形状为：

```text
D LEFT OUTER JOIN (Filter(...) 的子查询输入)
  GROUP BY D
  COUNT(CASE WHEN probe IS NOT NULL THEN 1 END)
```

真实匹配行的 `probe` 为 `1`，外连接补出的空行 `probe` 为 `NULL`。因此
`COUNT(*)` 在空组为 `0`；其他聚合的参数同样被探针保护，空组保持
`NULL`。非空 `COUNT(expr)`、`COUNT(DISTINCT expr)` 也遵循 SQL 的 NULL
忽略规则。

### MarkJoin 三值逻辑

`MarkJoin` 先取同一关联域下的子查询行，再计算比较结果：

- `EXISTS`：有行 `TRUE`，无行 `FALSE`。
- `IN` / `= ANY`：任一比较 `TRUE` 则为 `TRUE`；否则若有 `UNKNOWN` 则为
  `UNKNOWN`；否则 `FALSE`。
- `NOT IN`：对上述结果做三值否定。空子查询即使左值是 `NULL` 也为
  `TRUE`；非空且左值或子查询含 `NULL` 时为 `UNKNOWN`。
- `<> ALL` / 一般 `ALL`：任一比较 `FALSE` 则为 `FALSE`；否则若有
  `UNKNOWN` 则为 `UNKNOWN`；否则 `TRUE`。

`WHERE` 只接受严格 `TRUE`，`UNKNOWN` 不会被误当成通过。

### SingleJoin

标量子查询和普通 `SingleJoin` 都按左行分组右侧匹配：

- 0 行：右侧列补 `NULL`。
- 1 行：合并该行。
- 多于 1 行：抛 `CardinalityViolationError`，错误码
  `CARDINALITY_VIOLATION`，并带 `actualRows`。

## 主要 API

- `rewriteQuery(plan, context)` / `unnest(...)`：返回去相关后的 `Plan`。
- `executeUnnested(plan, context)`：重写并执行。
- `evaluatePlan(plan, context)` / `executeReference(...)`：逐行嵌套参考执行。
- `compareExecution(plan, context)`：同时执行两套引擎，返回
  `{ reference, rewritten, rewrittenPlan, equal, firstMismatch? }`。
- `assertExecutionEquivalent(plan, context)`：不等价时抛错。
- `freeColumns(plan, context)`：求当前层的自由关联列。
- `schemaOf(plan, context)`：静态输出 schema。
- `containsSubquery(plan)`：检查计划中是否仍有子查询表达式。

构造器包括 `table`、`filter`、`project`、`join`、`leftJoin`、`markJoin`、
`singleJoin`、`aggregate`、`union`、`distinct`、`limit`、`rowNumber`，
以及 `scalar`、`existsSubquery`、`notExistsSubquery`、`inSubquery`、
`notInSubquery`、`anyCompare`、`allCompare`。

## 已覆盖的验收场景

`test/unnesting.test.ts` 覆盖：

- 空组 `COUNT(*) = 0` 与 `SUM/AVG/MIN/MAX = NULL`。
- `NOT IN` 的 `NULL` 三值结果及空子查询返回 `TRUE`。
- 相关子查询逐外层行空集时的 `NOT IN ... = TRUE`。
- `EXISTS` / `NOT EXISTS` 与带 `OR` 析取条件的多列关联 `MarkJoin`。
- `= ANY`、`<> ALL`。
- 两层嵌套的相关标量聚合。
- `LIMIT` 的逐外层行分区语义。
- 标量多行时参考引擎与去相关引擎均抛基数异常。
- 根输出 schema 清理，不泄漏内部探针/标记列。

## 限制

- 非内连接 `ON` 条件中的子查询不会自动改写，会抛 `RewriteError`；把此类
  条件放到 `WHERE`/`Filter` 可正常处理。
- AST 不实现窗口函数、递归 CTE 或 SQL 解析；`Limit` 只支持数字
  `count/offset` 与可选排序键。
- 去相关化是逻辑重写；执行器使用嵌套循环连接以便验证语义，不负责生成
  物理哈希/归并连接算子。输出的普通 `Join` 节点已具备后续物理连接选择
  所需的键条件。
