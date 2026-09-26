# incremental-schema-validator

手写（零依赖、不借现成验证器）的增量 JSON 树验证器。树局部变化后，只重验
依赖该路径的检查项；跨字段约束通过依赖索引定位；每次补丁输出**新增**与**撤销**
的错误。每条错误同时携带**实例路径**（`instancePath`）与**规则路径**
（`schemaPath`，即规则在 schema 中的位置）。

## 承诺的 schema 子集

只承诺以下子集，其余关键字（`$ref`、`anyOf`、`additionalProperties`、`format`
等）明确不支持、会被忽略：

- `type`: `object | array | string | number | integer | boolean | null`
- `properties` / `required`（对象）
- `items`（数组，单一子 schema）
- `minimum` / `maximum`（仅对 number 生效）
- `minLength` / `maxLength` / `pattern`（仅对 string 生效）
- `enum`（深比较）
- `rules`（仅根节点）：跨字段约束 `{ kind: "lt" | "lte" | "eq", left, right }`，
  `left`/`right` 为绝对 JSON Pointer。任一操作数缺失时规则不适用（不产生错误）。

节点缺失时除 `required` 外的检查均不适用（与 JSON Schema 语义一致）。

## 增量设计

1. **编译**：schema 编译为一组检查项（`src/checks.ts`），每项登记它读取的实例
   路径依赖，依赖分两种失效模式（`src/depindex.ts`）：
   - `node`：只读该节点本身。变化路径是它的祖先或自身时失效。
   - `subtree`：读整棵子树（数组元素检查）。变化路径落在子树内（或包住它）时失效。
2. **依赖索引**：路径 trie。`query(变化路径)` 沿 trie 下行收集子树内全部依赖，
   上行只收集 `subtree` 模式依赖。因此改 `/x` 会重验 `x < y` 规则，但不会重验
   只依赖 `/z` 的检查；改 `/a/b` 也不会重验只读 `/a` 节点的类型检查。
3. **增量应用**（`src/validator.ts` 的 `IncrementalValidator.apply`）：先变更数据
   （`set` / `remove`），再经索引求受影响检查项集合，只重跑这些检查项，按错误键
   diff 出 `added` / `removed`。删除父节点时，子树上的检查项因祖先变化被重跑，
   目标缺失 → 错误自然撤销。
4. **一致性**：测试以全量 `validate()` 为 oracle，随机操作序列（固定种子）下逐步
   断言增量错误集与全量重算完全一致。

## API

```ts
const engine = new IncrementalValidator(schema, data);
engine.getErrors();                       // 当前错误集
const r = engine.apply([{ op: "set", path: "/x", value: 5 }]);
r.added; r.removed; r.errors; r.revalidated;  // 新增 / 撤销 / 全集 / 本轮重验的规则路径
validate(schema, data);                   // 一次性全量验证（oracle）
```

补丁操作：`{ op: "set", path, value }`（父节点必须已存在；数组允许末尾追加）、
`{ op: "remove", path }`（数组按 splice 语义，后续元素前移；目标不存在为 no-op）。

## 运行

需要 Node.js >= 23.6（原生 type stripping，直接运行 `.ts`，无需编译、无依赖）：

```sh
npm run demo   # 运行样例：局部修改 / 删除父节点 / 一致性核对
npm test       # Node.js 内置测试运行器
```
