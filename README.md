# SQL/JSON Path for PostgreSQL JSONB

一个无运行时依赖的 TypeScript `SQL/JSON Path` 解析、求值与索引下推库，面向 PostgreSQL `jsonb` 查询层。

实现重点：

- `lax` / `strict` 双模式。
- `lax` 成员访问递归解包嵌套数组。
- 过滤器内三值逻辑：`TRUE` / `FALSE` / `UNKNOWN`，且 `!UNKNOWN = UNKNOWN`。
- 比较运算的存在量化语义与空序列 `FALSE`。
- 从任意已支持 `jsonpath` 提取无假阴性的索引预筛计划。
- `jsonb_path_ops` 风格的 JSONB containment 输出，以及无法安全表达时的全扫描回退。

## 构建与测试

```bash
npm run build
npm test
```

构建产物在 `dist/`，包入口为 `dist/index.js`，类型声明为 `dist/index.d.ts`。

## 快速开始

```ts
import {
  evaluateJsonPath,
  extractIndexPlan,
  matchesIndexPlan,
  toJsonbContainment,
} from "./dist/index.js";

const document = {
  items: [
    { sku: "A-1", price: 12 },
    { sku: "B-2", price: 8 },
  ],
};

const values = evaluateJsonPath(
  "lax $.items[*] ? (@.price < 10).sku",
  document,
);
// ["B-2"]

const plan = extractIndexPlan(
  "lax $.items[*] ? (@.price < 10).sku",
);

// 用于在回表求值前裁剪候选文档。
const candidates = [document].filter((value) =>
  matchesIndexPlan(value, plan),
);

// 仅在计划可安全表示为 JSONB containment 时给出条件；
// 否则返回 { kind: "true" }，调用方应保留全扫描。
const containment = toJsonbContainment(plan);
```

## 传统 PostgreSQL 风格入口

```ts
import {
  jsonbPathExists,
  jsonbPathQuery,
  tryEvaluateJsonPath,
} from "./dist/index.js";

jsonbPathQuery({ a: [1, 2] }, "lax $.a[*] ? (@ > 1)");
// [2]

jsonbPathExists({ a: null }, "$.a");
// true

const result = tryEvaluateJsonPath("strict $.missing", {});
// {
//   ok: false,
//   mode: "strict",
//   values: [],
//   error: JsonPathEvaluationError { code: "MISSING_KEY", ... }
// }
```

## 语法支持

- 路径模式：`lax`、`strict`，默认 `lax`。
- 根与当前项：`$`、`@`。
- 成员与通配：`.key`、`."quoted key"`、`.*`。
- 数组：`[i]`、`[i, j to k]`、`[last]`、`[last - 1]`、`[*]`。
- 递归后代：`.**`。
- 过滤器：`? (expr)`。
- 布尔：`&&`、`||`、`!`。
- 比较：`==`、`!=`、`<`、`<=`、`>`、`>=`。
- 存在：`exists(path)`。
- 算术：`+`、`-`、`*`、`/`、`%` 及一元正负号。
- 正则：`value like_regex "pattern" flag "i"`，支持 `i`、`m`、`s`、`q`、`x`。
- 方法：`.type()`、`.size()`、`.double()`，并附带 `.abs()`、`.floor()`、`.ceiling()`。

`.size()` 返回数组长度；在 `lax` 下非数组项按一元素数组处理并返回 `1`，`strict` 下则是类型错误。

数组范围 `[i to j]` 为闭区间。

## 双模式语义

### `lax`

- 成员访问遇到数组时递归展开数组元素，再访问对象成员。
- 嵌套数组会逐层展开，例如：

  ```ts
  evaluateJsonPath("lax $.a.b", {
    a: [[{ b: 1 }], [[{ b: 2 }]]],
  });
  // [1, 2]
  ```

- 缺失键、非对象成员访问、越界索引均安静地产生空序列。
- 标量下标自动包装为一元素数组：`lax $.status[0]` 对 `{"status":"paid"}` 返回 `["paid"]`。
- `? (...)` 在应用谓词前展开一层输入数组。
- 比较运算在 `lax` 下展开一层数组操作数，因此数组可参与存在量化比较。

### `strict`

- 不自动展开数组。
- 缺失键、类型错误、越界索引等结构错误立即抛出结构化错误。
- 过滤器内部的错误会被捕获为 `UNKNOWN`，只淘汰当前候选，不中止整个路径求值。
- `!UNKNOWN` 仍为 `UNKNOWN`。

```ts
evaluateJsonPath("strict $.a.b", { a: [{ b: 1 }] });
// throws JsonPathEvaluationError { code: "MEMBER_ON_NON_OBJECT" }

evaluateJsonPath("strict $ ? (!(@.a == 1))", {});
// []
```

## 三值比较

比较对左右序列执行存在量化：

- 只要存在一对值满足比较，结果为 `TRUE`。
- 任一操作数序列为空时，结果为 `FALSE`，不是 `UNKNOWN`。
- 不可比较的类型产生类型错误；在过滤器中转为 `UNKNOWN`。
- `TRUE` 会支配同一比较中的其他错误候选。

```ts
evaluateJsonPath("lax $ ? (@.a == 1)", { a: [2, 1] });
// [{"a":[2,1]}]

evaluateJsonPath("lax $ ? (@.a == 1)", { a: [] });
// []

evaluateJsonPath("lax $ ? (!(@.a[*] == 1))", { a: [] });
// [{"a":[]}]
```

## 索引下推计划

`extractIndexPlan(path)` 返回以下节点：

```ts
type IndexPlan =
  | { kind: "all"; reason?: string }
  | { kind: "term"; term: IndexTerm }
  | { kind: "and"; children: IndexPlan[] }
  | { kind: "or"; children: IndexPlan[] };

interface IndexTerm {
  test: "exists" | "equals";
  path: IndexPathSegment[];
  value?: JsonValue;
  source?: string;
}

type IndexPathSegment =
  | { kind: "key"; key: string }
  | { kind: "index"; index: number }
  | { kind: "indexRange"; from: number; to: number }
  | {
      kind: "arraySelection";
      wildcard: boolean;
      selectors: ArrayIndexSelector[];
      wrapScalar?: boolean;
    }
  | { kind: "anyIndex" }
  | { kind: "anyKey" }
  | { kind: "laxUnwrap" }
  | { kind: "recursive" };
```

规则：

- `AND` 合并为合取计划。
- `OR` 在各分支都有安全事实时生成析取计划。
- 任一 `OR` 分支无法安全提取时，退回 `{ kind: "all" }`。
- `!expr` 不贡献任何正向索引事实。
- `lax` 数组展开以 `laxUnwrap` 显式保留，绝不退化成错误的固定嵌套键路径。
- 多态/数组比较在 `lax` 下使用“标量相等或数组元素相等”的析取项。
- 路径方法后的当前项不再等价于原始文档位置，因此不会继续做不安全下推。

### 无假阴性保证

对任意已支持路径 `p` 和文档 `d`：

```ts
if (evaluateJsonPath(p, d).length > 0) {
  matchesIndexPlan(d, extractIndexPlan(p)); // 必定为 true
}
```

索引计划只是必要条件预筛，不是精确匹配器；命中后仍应执行完整 `jsonpath` 求值。

### GIN containment

`toJsonbContainment(plan)` 将可表达的部分转换为：

```ts
type ContainmentQuery =
  | { kind: "true" }
  | { kind: "contains"; value: JsonValue }
  | { kind: "and"; children: ContainmentQuery[] }
  | { kind: "or"; children: ContainmentQuery[] };
```

- 简单严格键路径可以编译为嵌套 `@>` 参数。
- 数组通配可编译为 JSONB array containment。
- `laxUnwrap`、递归、任意键、非通配数组下标和方法结果等无法安全表达时，返回 `{ kind: "true" }`。
- 调用方可将 `contains` 节点映射为 `jsonb_column @> $1`，并按 `and` / `or` 组合；遇到 `true` 时保留全量扫描。

## 主要 API

- `parseJsonPath(source, options?)` / `parse` / `compile`
- `evaluateJsonPath(pathOrAst, document, options?)` / `evaluate` / `query`
- `tryEvaluateJsonPath(pathOrAst, document, options?)`
- `evaluateJsonPathResult(document, pathOrAst, options?)`
- `jsonPathExists(path, document, options?)`
- `jsonbPathQuery(document, path, options?)`
- `jsonbPathExists(document, path, options?)`
- `extractIndexPlan(pathOrAst, options?)`
- `extractIndexTerms(pathOrAst)`
- `matchesIndexPlan(document, plan)` / `indexPlanMatchesDocument`
- `matchesPlan(plan, document)`
- `toJsonbContainment(plan)` / `toContainmentQuery`
- `jsonEqual`, `jsonType`, `isJsonArray`, `isJsonObject`

错误类型：

- `JsonPathParseError`：`code = "PARSE_ERROR"`。
- `JsonPathEvaluationError`：包含 `code`、`mode`、`offset`、`details`。

## 测试覆盖

- `lax` 多层数组展开与 `strict` 结构错误。
- 过滤器内缺失键/类型错误转 `UNKNOWN`，取反保持 `UNKNOWN`。
- 空序列比较、数组存在量化比较。
- `last`、`last - 1`、闭区间和索引列表。
- `type()`、`size()`、`double()`、算术与 `like_regex`。
- 随机嵌套 JSON 文档上的索引计划零假阴性对照测试。
- strict containment 输出的随机假阴性对照测试。
