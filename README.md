# schema-sampler

为受限 JSON Schema 子集生成**正例**与**逐条规则反例**的 TypeScript 库。
零依赖，使用 Node.js 内置测试运行器（`node:test`）验证。

## 运行要求

- Node.js ≥ 23.6（利用原生 TypeScript type-stripping 直接运行 `.ts`，无需编译/安装）

```bash
node src/demo.ts          # 运行演示
node --test test/*.test.ts  # 运行全部测试
```

## 设计要点

- **只承诺已实现的子集**（见下），未实现的一律不声明支持。
- **独立验证器**（`src/validator.ts`）从零实现，不借 ajv 等现成验证器；
  生成器产出的每个样例都用它验真。
- **错误同时携带实例路径与规则路径**：`{ instancePath: "/age", schemaPath: "/properties/age/minimum" }`。
- **关联约束联动**：同一节点上的约束联合求解（如 `minimum`/`maximum`/`multipleOf`/`integer`
  共同决定取值区间；`pattern` 与 `minLength`/`maxLength` 共同决定字符串）。
- **反例只违反指定规则**：每个反例生成后用独立验证器复核，必须恰好产生 1 个错误且
  `schemaPath` 等于目标规则路径，否则不输出。
- **无法构造时说明原因而非伪造**：矛盾 schema 无正例；无法隔离的违反（如
  `{const:5, minimum:5, maximum:5}` 中违反 `const`）给出原因。
- **seed 相同结果稳定**：全部随机性来自 mulberry32 种子 PRNG。

## 支持的 schema 子集

- `type`: `object | array | string | number | integer | boolean | null`
- `const`, `enum`
- 数值：`minimum`, `maximum`, `exclusiveMinimum`, `exclusiveMaximum`, `multipleOf`
- 字符串：`minLength`, `maxLength`, `pattern`
- 数组：`items`（单 schema）, `minItems`, `maxItems`
- 对象：`properties`, `required`, `additionalProperties: false`

`pattern` 的**生成**子集（验证始终使用真实 `RegExp`）：锚定 `^...$`、字面量、
`[...]` 字符类（含区间与取反）、`\d \w \s`、`.`、量词 `* + ? {n} {n,m} {n,}`、
字面量交替 `(cat|dog)`。超出该子集的 pattern 会明确报告"无法构造"并说明原因。

## 模块

- `src/validator.ts` — 独立验证器，错误含实例路径 + 规则路径
- `src/generator.ts` — 正例生成（联动约束求解 + 不可满足检测）
- `src/counterexample.ts` — 逐规则反例引擎（生成后验真）
- `src/regex-lite.ts` — 受限正则子集的解析与采样
- `src/rng.ts` — 种子 PRNG
- `src/demo.ts` — 可运行演示

## 演示输出摘录

```
positive sample: { "name": "Pl", "age": 128, "score": 7, "tags": ["green"], "active": true }
independent validator: CLEAN (0 errors)

rule /properties/age/minimum
  instance: { "name": "Wg", "age": -1, "score": 1.5, "tags": ["green","red","green"], "active": true }
  verified single error -> instancePath "/age", schemaPath "/properties/age/minimum"

rule /properties/name/minLength
  cannot construct: no value violates "minLength" while satisfying the other constraints
  (pattern ^[A-Z][a-z]+$ forces length >= 2)

contradictory schema { minimum: 10, maximum: 5 }
  reported unsatisfiable: empty numeric range [10, 5]

two runs with seed 42 identical: true
```

## 测试结果

33 个测试全部通过（验证器路径、正例验真、反例单点违反、矛盾检测、确定性）：

```
ℹ tests 33
ℹ pass 33
ℹ fail 0
```
