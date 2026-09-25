# Versioned Field Lineage

一个零运行时依赖的 TypeScript 示例，用不可变版本图保存 JSON 关系数据在筛选、拆分、聚合和连接后的字段级血缘。

## 语义

- **输入**：JSON 兼容树；对象键无序，数组顺序保留。
- **路径**：使用 JSON Pointer，`/data/0/name` 表示数组元素；`~` 与 `/` 分别转义为 `~0`、`~1`。
- **关系位置**：`loadTree(tree, { rowsPath: "/data" })` 从树中取得行数组；行内路径是 `/0/field`，原始证据路径自动保留为 `/data/0/field`。
- **多对多映射**：每个输出字段保存直接父节点引用，也保存追溯到源输入的全部 `{ source, version, path }`。
- **去重规则**：按来源名、源版本和源路径精确去重；相等值只要来自不同路径，就保留为不同证据。
- **版本查询**：每个操作生成新版本；可用 `field`、`parents`、`origins`、`explain`、`entries` 和 `versionNames` 查询。
- **过滤**：未通过谓词的行不会进入输出版本，因此也不会出现在后续任何血缘路径中。
- **拆分**：数组的每个元素生成一行，数组下标进入路径，避免同值证据合并。
- **聚合**：聚合字段追溯到所有贡献字段；`count` 追溯到所有贡献行。
- **连接**：内连接按键做多对多笛卡尔展开，输出同时保留左、右两侧字段血缘。
- **错误定位**：无效输入、缺失路径、谓词错误和聚合类型错误都抛出带 `code`、`version`、`path`、`locations` 的 `LineageError`。

## 环境

需要 Node.js `>= 22.6.0`，使用内置 TypeScript 类型擦除和内置测试运行器，无需安装依赖。

```bash
node --version
npm test
npm run demo
```

如果系统默认 Node 仍是旧版本，可以直接使用新版本路径：

```bash
/opt/homebrew/bin/node --test --experimental-strip-types src/*.test.ts
/opt/homebrew/bin/node --experimental-strip-types src/demo.ts
```

## API

```ts
import {
  loadTree,
  attachTree,
  filterRows,
  splitRows,
  aggregateRows,
  joinRows,
} from "./operations.ts";
```

### 载入源树

```ts
const orders = loadTree(input, {
  source: "orders.json",
  version: "orders-v1",
  rowsPath: "/data",
});
```

### 筛选

```ts
const active = filterRows(orders, {
  version: "active-v1",
  predicate: (row, ctx) => ctx.at("/active") === true,
});
```

谓词的 `ctx.at("/field")` 读取值；`ctx.ref("/field")` 可取得对应版本路径。谓词内错误会包装为 `CALLBACK_ERROR`，同时保留原因错误的具体路径。

### 拆分

```ts
const items = splitRows(active, {
  version: "items-v1",
  field: "items",
  as: "item",
});
```

原 `items` 数组字段会被提升后的 `item` 字段替换，避免同一对象同时以数组和标量字段存在。

### 聚合

```ts
const totals = aggregateRows(items, {
  version: "totals-v1",
  groupBy: "item/sku",
  keyAs: "sku",
  aggregates: [
    { as: "quantity", op: "sum", field: "item/qty" },
    { as: "matchedRows", op: "count" },
  ],
});
```

支持 `count`、`sum`、`avg`、`min`、`max`、`collect`。除 `count` 外都需要 `field`。

### 多源连接

```ts
const customers = attachTree(totals.lineage, customerTree, {
  source: "customers.json",
  version: "customers-v1",
  rowsPath: "/data",
});

const joined = joinRows(items, customers, {
  version: "joined-v1",
  leftKey: "customerId",
  rightKey: "id",
  leftAs: "order",
  rightAs: "customer",
});
```

两侧必须共享同一个 `Lineage` 图；相同键的重复行会自然产生多对多匹配，不按键值合并证据。

### 查询血缘

```ts
totals.lineage.versionNames();
totals.lineage.parents("totals-v1", "/0/quantity");
totals.lineage.origins("totals-v1", "/0/quantity");
totals.lineage.field("totals-v1", "/0/quantity");
totals.lineage.entries("totals-v1");
totals.lineage.explain("totals-v1", "/0/quantity");
```

## 运行样例摘要

`src/demo.ts` 的管线为：

```text
orders-v1 → active-orders-v1 → order-items-v1 → item-customers-v1 → sku-totals-v1
                         customers-v1 ↗
```

最终输出：

```json
[
  { "sku": "a", "quantity": 5, "matchedRows": 3 },
  { "sku": "b", "quantity": 2, "matchedRows": 2 }
]
```

`/0/quantity` 的原始证据只有两个订单数量字段，重复客户匹配不会重复同一字段证据：

```json
[
  { "source": "orders.json", "version": "orders-v1", "path": "/data/0/items/0/qty" },
  { "source": "orders.json", "version": "orders-v1", "path": "/data/1/items/0/qty" }
]
```

## 最近一次测试结果

```text
▶ versioned field lineage
  ✔ keeps source paths under nested array indexes after filter and split
  ✔ does not include filtered rows in any reachable lineage entry
  ✔ aggregates an output field to every contributing source field
  ✔ joins many-to-many and preserves both left and right sources
  ✔ deduplicates exact source references but never merges equal values from different paths
  ✔ keeps split item evidence separate from a retained field with the same value
  ✔ gives an empty filtered relation a root with no source evidence
  ✔ maps every collected aggregate array element to its contributing field
  ✔ supports querying by version and composing direct parent evidence
  ✔ handles escaped JSON pointer object keys
  ✔ locates invalid input and callback errors at concrete paths
  ✔ locates a missing source rows path with source and version
  ✔ locates aggregate type errors at the contributing field
ℹ tests 13
ℹ pass 13
ℹ fail 0
```
