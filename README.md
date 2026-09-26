# incremental-schema-validator

增量 JSON 树校验器（TypeScript，零依赖，不借用任何现成验证器）。局部修改只重验
依赖被改路径的规则；跨字段约束通过依赖索引精确触发；每次变更输出**新增**与
**撤销**的错误差集；每条错误同时携带**实例路径**与**规则（schema）路径**。

## 运行

需要 Node.js >= 23.6（原生 TypeScript 类型擦除，无需编译、无需安装依赖）。

```sh
npm run demo   # 运行样例：examples/demo.ts
npm test       # Node 内置测试运行器：test/incremental.test.ts
```

## 承诺的 schema 子集（仅这些，未列出的一律不支持）

- `type`: `"object" | "array" | "string" | "number" | "integer" | "boolean"`
- object：`properties`、`required`、`rules`（跨字段数值比较）
- array：`items`、`minItems`、`maxItems`
- string：`minLength`、`maxLength`、`pattern`
- number/integer：`minimum`、`maximum`、`exclusiveMinimum`、`exclusiveMaximum`
- 任意节点：`enum`（含数组/对象的深比较）

跨字段规则写在 object 节点上，比较两个数值型后代字段，仅当两端都存在且为
有限数值时生效：

```ts
{ type: 'object',
  properties: { start: { type: 'number' }, end: { type: 'number' } },
  rules: [{ op: 'lt', left: 'start', right: 'end' }] }
```

`op` 支持 `lt | lte | gt | gte | eq`；`left`/`right` 是相对该 object 的指针。

## 工作机制

1. **编译**：schema 与文档联合遍历，展开成逐实例的规则（`src/expand.ts`）。
   每条规则声明自己读取的实例路径 `deps`（如 `#/rules/0` 的 deps 是
   `/start`、`/end`）。
2. **依赖索引**：`实例路径 -> 规则集合`（`src/validator.ts`）。浅依赖（类型、
   区间、跨字段比较等）只在写入路径位于依赖路径之上或相等时重验；深依赖
   （`enum` 深比较）在写入路径与依赖路径互为前缀时重验。改 `/start` 会重验
   `start < end`，但不会碰 `/age`、`/name` 的规则。
3. **撤销与重建**：被影响的规则先注销（其错误进入 removed），再按 schema 在
   受影响作用域重新展开、只执行新建规则（其错误进入 added）。删除父节点时，
   子树规则依赖位于父路径之下，随之注销，子错误即被撤销。数组元素的增删会
   使后续下标前移，因此数组内变更从数组容器路径整体重验。
4. **差集输出**：`update()/remove()` 返回 `{ added, removed }`，按
   `(instancePath, schemaPath, message)` 做净差集。
5. **一致性**：`validateFull()` 每次从零展开全部规则独立校验；测试保证任意
   操作序列后 `getErrors()`（增量）与 `validateFull()`（全量）完全一致。

## API

```ts
import { IncrementalValidator, DELETE } from './src/index.ts';

const v = new IncrementalValidator(schema, doc);
v.getErrors();                 // 当前增量错误集
v.validateFull();              // 从零全量校验（基准）
v.update('/start', 3);         // 局部修改 -> { added, removed }
v.update('/tags', DELETE);     // 删除节点（或 v.remove('/tags')）
v.replaceDocument(newDoc);     // 整体替换 + 全量校验
v.lastExecuted;                // 上一次变更实际执行的规则 id（可观测增量性）
```

错误结构：
`{ instancePath: '/address/zip', schemaPath: '#/properties/address/properties/zip/pattern', message: '...' }`。

## 已知边界（子集之外，明确不承诺）

- 不支持 `additionalProperties`、`patternProperties`、`$ref`、组合关键字
  （`allOf/anyOf/oneOf/not`）、字符串 `format`、数组 `uniqueItems` 等。
- 跨字段规则仅支持数值比较，且两端缺失或非数值时视为不适用（不报错）。
- 依赖索引当前是线性扫描的路径表；大规模文档可替换为路径 trie，接口不变。
