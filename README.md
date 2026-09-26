# sample-schema-inference

从样本归纳 schema **候选集**（而非单个看似确定的结论），全程保留证据计数与来源，
不借任何现成验证器。纯 TypeScript，零依赖，Node.js >= 23 直接运行。

## 运行

```sh
npm test     # Node 内置测试运行器 (node:test)
npm run demo # 运行样例 examples/demo.ts
```

## 设计原则

- **证据计数**：每个字段分别统计 present / missing / null（缺失与 null 分开），
  数值与字符串范围记录贡献边界的样本 id（provenance）。
- **候选集而非定论**：每个决策点（type / presence / nullability / range / length）
  输出按「样本支持度 → 简约度」排序的多个候选，并给出区分相邻候选的最小测试样例
  （distinguisher），例如 integer vs number 用 `{"age": 55.5}` 区分。
- **样本归纳 ≠ 普遍保证**：只见整数不承诺未来无小数（标 `inductive`）；
  归纳性结论单列在 `inductions`，`guarantees` 只承诺已实现并独立复核的子集。
- **开放世界**：样本未见的字段不被永久禁止（不生成 additionalProperties 禁令）；
  未观察领域（全 null 字段、空数组元素、空样本集）标 `unknown`。
- **可撤回**：增删样本后证据从保留样本重算，撤回异常样本后范围自动收缩，
  来源计数随之更新。
- **独立验证**：所有保留样本用内置手写验证器对 primary schema 重新校验并报告计数。
- **受限子集**：验证器只实现 `type, properties, required, items, minimum, maximum,
  minLength, maxLength`；错误同时给出 `instancePath` 与 `rulePath`。

## API

```ts
import { SchemaInference, validate } from './src/index.ts';

const inf = new SchemaInference();
inf.addSample('u1', { name: 'ada', age: 36 });
inf.addSample('u2', { name: 'grace', age: 41 });
inf.retractSample('u2');
const report = inf.report();
// report.decisionPoints / report.primary / report.unknowns
// report.verification / report.guarantees / report.inductions
const errors = validate({ age: 3.5 }, report.primary);
// errors[0].instancePath === '/age', errors[0].rulePath === '#/properties/age/type'
```
