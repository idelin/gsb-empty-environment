# schema-sample-gen

受限 JSON Schema 子集的**正例 / 单规则反例**生成器。TypeScript 编写，零依赖，
使用 Node.js 内置测试运行器（`node:test`）验证。

## 设计原则

- **只承诺实现的子集**：超出子集的关键字（`$ref`、`anyOf`、`format` 等）直接抛
  `UnsupportedSchemaError`，绝不静默忽略。
- **独立验证器验真**：`src/validator.ts` 是手写验证器，不借 ajv 等现成验证器，
  也不复用生成器逻辑；生成器产出的每个正例、反例都经它验真后才返回。
- **错误双路径**：每个验证错误同时给出实例路径（`instancePath`，JSON Pointer）
  与规则路径（`schemaPath`，`#` 开头）。
- **反例只违反指定规则**：其余规则尽量保持满足；产出经独立验证器确认
  "违规集合恰好等于目标规则"，否则重试或放弃。
- **无法构造就明说**：矛盾 schema 无正例、原理上不可构造的反例（如
  `integer` + `multipleOf: 1`），都返回原因，绝不伪造。
- **关联约束联动**：`dependentRequired` 触发属性出现时自动补齐依赖属性；
  构造反例省略属性时会连带分析联动牵连。
- **种子确定性**：相同 seed 产出完全一致（mulberry32 PRNG）。

## 支持的 schema 子集（只承诺这些）

| 类别 | 关键字 |
| --- | --- |
| 通用 | `type`（单一类型）、`enum`、`const` |
| string | `minLength`、`maxLength`、`pattern`（受限子集，见下） |
| number/integer | `minimum`、`maximum`、`exclusiveMinimum`、`exclusiveMaximum`、`multipleOf` |
| array | `items`（单一 schema）、`minItems`、`maxItems`、`uniqueItems` |
| object | `properties`、`required`、`additionalProperties`（仅布尔）、`dependentRequired` |

`pattern` 子集：必须 `^...$` 全串锚定；支持字面字符、字符类（含范围与 `[^...]`
取反）、`\d \w \s` 转义、`.`、量词 `* + ? {m} {m,} {m,n}`；**不支持**分组与 `|`。

## 运行环境

- Node.js ≥ 23.6（原生运行 TypeScript）；或 Node ≥ 22.6 加
  `--experimental-strip-types`。
- 仓库无第三方依赖，无需 `npm install`。

```bash
nvm use 23        # 或任何满足版本要求的 Node
npm test          # node --test test/*.test.ts
npm run demo      # node src/demo.ts
```

## API 示例

```ts
import { generate, generateCounterexample, listRules, validate } from './src/index.ts';

const schema = {
  type: 'object',
  properties: {
    name: { type: 'string', minLength: 2, maxLength: 10 },
    age: { type: 'integer', minimum: 0, maximum: 150 },
    vip: { type: 'boolean' },
    level: { type: 'integer', minimum: 1, maximum: 10 },
  },
  required: ['name', 'age'],
  additionalProperties: false,
  dependentRequired: { vip: ['level'] },
};

// 正例：{ ok: true, value } 或 { ok: false, reason }（矛盾 schema）
const positive = generate(schema, 42);

// 反例：只违反指定规则；不可构造时 { ok: false, reason }
const ce = generateCounterexample(
  schema,
  { schemaPath: '#/properties/age/minimum', keyword: 'minimum' },
  42,
);

// 枚举所有可针对的规则
const rules = listRules(schema);

// 独立验证器：错误带 instancePath + schemaPath
const errors = validate({ name: 'A', age: 200 }, schema);
```

## 运行样例（`npm run demo`，节选）

```
seed = 20261004（相同 seed 结果完全一致）

=== 正例（满足全部规则） ===
{
  "name": "rTxx",
  "age": 28,
  "level": 6
}
独立验证器验真: 通过，0 个错误

=== 单规则反例（只违反指定规则，其余保持满足）===

[#/properties/age/minimum]
  实例: {"name":"rTxx","age":-1,"level":2}
  违规: instancePath="/age" schemaPath=#/properties/age/minimum (minimum): -1 < minimum 0

[#/dependentRequired]
  实例: {"name":"rTxx","age":28,"vip":false}
  违规: instancePath="" schemaPath=#/dependentRequired (dependentRequired): 属性 "vip" 存在时要求 "level" 也出现

[#/properties/tags/uniqueItems]
  实例: {"name":"Hdk","age":89,"email":"s@pg.com","vip":false,"level":5,"tags":["06mr","06mr"]}
  违规: instancePath="/tags" schemaPath=#/properties/tags/uniqueItems (uniqueItems): 元素 [0] 与 [1] 重复

=== 矛盾 schema（minLength=5 > maxLength=2） ===
无正例: #/properties/id: minLength(5) > maxLength(2)，长度区间为空

=== 不可构造的反例（integer 且 multipleOf=1） ===
无法构造: #: 其余约束下不存在非 1 倍数的取值（例如 integer 且 multipleOf=1 时原理上不可违反）
```

完整输出见 `node src/demo.ts`。

## 测试结果（`npm test`）

```
✔ 正例生成：多种 schema × 多种 seed 均通过独立验证器验真
✔ 相同 seed 结果完全一致（确定性）
✔ dependentRequired 联动：触发属性出现时依赖属性必被补齐
✔ 矛盾 schema 无正例：返回原因而非伪造
✔ 超出子集的 schema 抛错而非静默处理
✔ 每条规则都能造出"只违反该规则"的反例，并经独立验证器验真
✔ 反例确定性：相同 seed 结果一致
✔ 原理上不可构造的反例：说明原因而非伪造
✔ 矛盾 schema 的反例请求：报告无正例
✔ 反例确实满足其他所有规则（抽查具体实例）
✔ 错误同时携带实例路径与规则路径（嵌套）
✔ 数组元素的实例路径带下标
✔ required / additionalProperties / dependentRequired 的规则路径
✔ 数值与字符串约束
✔ uniqueItems 与 enum/const
✔ 超出承诺子集的关键字直接拒绝，不静默忽略
ℹ tests 16
ℹ pass 16
ℹ fail 0
```

## 目录结构

```
src/
  schema.ts          # 子集定义 + 子集外关键字拒绝
  rng.ts             # mulberry32 确定性 PRNG
  pattern.ts         # 受限 pattern 子集的解析与生成
  validator.ts       # 独立验证器（双路径错误）
  generator.ts       # 正例生成 + 矛盾检测
  counterexample.ts  # 单规则反例生成
  demo.ts            # 演示脚本
test/
  validator.test.ts
  generator.test.ts
  counterexample.test.ts
```

## 已知限制

- 不支持 `$ref` 与组合关键字（`allOf/anyOf/oneOf/not`）、条件关键字（`if/then/else`）。
- `uniqueItems` 的取值域大小只对 `boolean`/`null`/`enum`/`const`/有界 `integer`
  做精确估计；其余按无限域处理，极端情况下靠有界重试发现不可行并报告。
- 数值生成在区间过宽时收缩到 ±1000 附近采样（确定性不受影响）。
