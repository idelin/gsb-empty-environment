# 可逆事实演绎推理与最小解释生成器

基于霍恩子句（Horn Clauses）的正向链推理器，TypeScript 实现，
零依赖，使用 Node.js（>= 23.6，原生 TypeScript 类型擦除）直接运行。

## 功能

- **正向链推理**：`addFact` / `addRule(antecedents, conclusion)`，新事实或新规则即刻触发推导。
- **证明树**：每个派生结论记录全部正当理由（justification），形成证明 DAG。
- **最小解释**：`explain(fact)` 返回基础事实证据集最小（并列时深度最浅）的最短证据链证明树。
- **可逆撤销**：`retractFact(fact)` 撤销基础事实后，从剩余公理重算最小不动点闭包，
  级联清除失去全部独立证据的派生结论（含无根基循环互证的正确清除）。

## 运行

```bash
npm run demo   # 运行验收场景样例
npm test       # node:test 内置测试（13 个用例）
```

## API

```ts
import { ReasoningEngine } from "./src/engine.ts";

const e = new ReasoningEngine();
e.addFact("A");
e.addRule(["A"], "B");      // A -> B
e.isKnown("B");             // true
e.explain("B");             // { proof, baseFacts: ["A"], depth: 1 }
e.retractFact("A");         // 级联清除 B
```

## 验收场景输出（`npm run demo`）

```
=== 步骤 1: 断言 A 与规则 A->B ===
当前成立的事实: [A, B]
B 的最小解释: 证据集 {A}, 推理深度 1

=== 步骤 2: 加入 C 与 C->B 后撤销 A（B 因 C 保留） ===
当前成立的事实: [B, C]
B 的最小解释: 证据集 {C}, 推理深度 1

=== 步骤 3: 再撤销 C（B 失去全部支持被清除） ===
当前成立的事实: []
B 不成立，无解释。
```

## 测试结果（`npm test`）

```
ℹ tests 13
ℹ pass 13
ℹ fail 0
```

覆盖：三条验收边界、多级链式推导与级联清除、多前件规则、菱形依赖、
最小解释择优与次优回退、撤销后重新断言恢复、规则先于事实、幂等性、
环形规则终止性与无根基环清除、空前件规则、未知事实查询。

## 设计要点

- 内部为理由维护系统（JTMS）：事实只要存在至少一条当前有效的理由即成立。
- 撤销采用“重算最小不动点闭包”而非朴素级联，保证良基性——
  `A->B, B->A` 这类无根基循环互证在失去外部支持后也会被清除。
