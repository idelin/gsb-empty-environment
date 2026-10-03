# json-columnar-projector

结构化 JSON 扁平化列式投影器（TypeScript，零依赖）。

动态拆解半结构化 JSON：自动推断字段路径，把同一路径的值抽取到独立的
虚拟列中；当查询只涉及少数叶子路径时，通过选择性扫描器直接从原始 JSON
文本中物化目标路径，**跳过并永不反序列化**无关子树（如订单商品列表）。

## 环境要求

- Node.js >= 22.6（利用原生 TypeScript 类型擦除直接运行 `.ts`，无需编译；
  开发验证使用 v23.9.0）

## 快速开始

```bash
npm test        # Node.js 内置测试运行器 (node:test)
npm run demo    # 订单投影演示
```

## 核心概念

| 模块 | 职责 |
| --- | --- |
| `src/path.ts` | 路径解析/格式化：`user.address.city`、`items[0].price` |
| `src/flatten.ts` | `flattenJson` 把文档拆成叶子路径→值；`inferPaths` 推断虚拟列 schema |
| `src/selective.ts` | `extractPathsFromRaw` 选择性流式抽取：只物化目标路径，其余子树按原始字符扫描跳过 |
| `src/projector.ts` | `ColumnarProjector`：每个叶子路径一个独立虚拟列，`query(paths)` 只读取指定列 |

## 用法示例

```ts
import { ColumnarProjector } from "./src/index.ts";

const projector = new ColumnarProjector();

// 1) 推断虚拟列 schema
projector.inferPaths(sampleOrder); // -> ['user.address.city', 'items[0].price', ...]

// 2) 选择性摄取原始 JSON：只物化 'user.address.city'，
//    items[]/payment/logistics 等子树被原始扫描跳过，从不反序列化
projector.addRowSelective(rawOrderJson, ["user.address.city"]);

// 3) 列式查询：只读取指定虚拟列
const { columns } = projector.query(["user.address.city"]);
// columns["user.address.city"] -> ["Hangzhou", "Shanghai", ...]
```

## 运行样例（`npm run demo`）

```
=== 1. Inferred virtual columns (schema of first order) ===
channel
items[0].attrs.color
...
user.address.city
user.address.district
...

=== 2. Selective ingestion: only 'user.address.city' ===
rows=3 materializedValues=3 skippedValues=36

=== 3. Query only the city column ===
rowCount=3
user.address.city -> [ 'Hangzhou', 'Shanghai', 'Ningbo' ]

=== 4. Cost comparison vs full JSON.parse ===
full parse leaf values per order : 39
selective materialized per order : 1
=> items[], payment, logistics subtrees were skipped by raw scan, never deserialized.
```

提取城市维度时，每行仅物化 1 个值（城市字符串），而完整
`JSON.parse` + 扁平化需要构造全部 39 个叶子值；订单商品列表等
36 个无关值被扫描器直接跳过。

## 测试结果（`npm test`，Node v23.9.0）

```
ℹ tests 21
ℹ pass 21
ℹ fail 0
```

覆盖范围：

- `test/path.test.ts`：路径解析/格式化往返、非法路径报错
- `test/flatten.test.ts`：嵌套订单拆解、schema 推断、空容器保留
- `test/selective.test.ts`：选择性抽取与 `JSON.parse` 真值一致、
  仅物化 1 个值、跳过数组兄弟元素、转义/Unicode 跳过区、子树物化、
  缺失路径报告、标量往返、畸形 JSON 报错
- `test/projector.test.ts`：虚拟列构建、选择性摄取、按需列查询、
  缺失路径的空槽位

## 设计说明

- **路径即列**：对象键用 `.` 连接，数组元素用 `[index]`，如
  `items[1].price`；同一文档中相同路径的值归入同一虚拟列。
- **投影下推**：`extractPathsFromRaw` 预计算目标路径集合及其祖先
  集合；扫描时不在祖先链上的子树用纯字符扫描跳过（正确处理字符串
  转义），不分配任何对象/数组/标量。
- **可验证的跳过**：每次摄取返回 `ExtractStats`
  （`materializedValues` / `skippedValues` / `missingPaths`），
  测试据此证明无关结构未被反序列化。
