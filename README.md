# 查询网关静态预算

`src/index.ts` 提供无依赖的 GraphQL 静态预算组件。它在请求发出前解析整个操作，不依赖实际响应，也不会把超限总量拆成多个游标分页请求。

## 使用

```ts
import { estimateBudget, assertWithinBudget } from "./src/index.ts";

const request = {
  query: `
    query Feed($size: Int!) {
      users(first: $size) {
        nodes {
          id
          posts(first: 5) {
            nodes { title }
          }
        }
      }
    }
  `,
  variables: { size: 20 },
};

const result = estimateBudget(
  request,
  { maxCost: 2_000, maxDepth: 8, maxPageSize: 100 },
);

if (!result.ok) {
  console.error(result.path, result.reason);
}

// 需要异常语义时使用：
assertWithinBudget(request, { maxCost: 2_000 });
```

## 成本模型

每个字段的成本为：

```text
fieldWeight × depth^depthWeight × enclosingPageMultiplier
```

- `depth` 从操作根选择集开始为 `1`。
- `enclosingPageMultiplier` 是所有外层列表分页大小的乘积。
- 字段自身的分页参数只乘其后代，不乘字段自身。
- 默认分页参数为 `first`、`last`、`limit`、`take`、`pageSize`、`perPage`；可用 `pageSizeArguments` 覆盖。
- 同一层多个分页参数同时出现时取最大值，做保守估算。
- `after`、`before`、`cursor` 等游标参数只记录，不改变成本。

片段在同一个选择集中按片段定义去重。重复展开、以及两个中间片段共同引用同一片段，都只计费一次；同一片段被不同响应分支引用时，因属于不同响应选择集，仍分别计费。片段环会被拒绝。

分页变量和 `@include` / `@skip` 指令变量会从 `variables` 与操作变量默认值解析。分页变量缺失、非整数或负数时拒绝请求。

## 结果

`BudgetResult` 的关键字段：

- `ok` / `allowed` / `rejected`：是否通过。
- `cost` / `totalCost`：完整估算成本。
- `path` / `pathSegments`：首个违规路径；预算超限时 `overBudgetPath` 指向首次越过预算的字段。
- `violations`：`BUDGET_EXCEEDED`、`DEPTH_EXCEEDED`、`PAGE_SIZE_EXCEEDED`、`UNRESOLVED_VARIABLE`、`CURSOR_FALLBACK` 等结构化原因。
- `breakdown`：逐字段的深度、分页倍数、字段成本与累计成本。
- `fragmentExpansions`：实际计费的片段展开顺序，用于验证去重。
- `cursor.independent`：成本与游标位置无关。

`cursorFallback: true`、`requests > 1` 或 `paginationMode: "cursor" / "cursor-fallback"` 会直接产生 `CURSOR_FALLBACK` 拒绝，不能借分页摊薄总预算。

## 验证

```bash
npm test
```

测试覆盖深链超限路径、嵌套列表倍数、片段直接/传递复用、不同分支计费、分页与指令变量传播、深度/页大小限制、游标回退拒绝以及结构化异常。
