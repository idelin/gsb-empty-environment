# 可逆事实演绎推理与最小解释生成器

基于霍恩子句(Horn Clauses)的正向链推理器,TypeScript 实现,零依赖,
使用 Node.js 内置测试运行器(Node ≥ 22.6,利用原生 TypeScript 类型擦除直接运行 `.ts`)。

## 功能

- **正向链推理**:断言基础事实后,自动触发所有前提满足的霍恩规则,推导新结论;
  规则可在事实之后注册,补触发已有事实。
- **证明树**:每条派生事实记录其全部正当理由(规则 + 前提集合),
  同一结论可拥有多条独立支持。
- **最小解释**:`explain(fact)` 沿证明树回溯,返回最短事实证据链
  (推导深度最小;含基础事实集合与拓扑序推导步骤),并防御循环证明。
- **可逆撤销(真值维护)**:`retract(fact)` 撤销基础事实后,
  级联清除失去全部独立支持的派生结论;仍有其他支持的结论及其下游保留。

## 运行

```bash
npm test    # 运行 node:test 测试(12 个用例)
npm run demo  # 运行验收场景样例
```

## API 摘要

```ts
import { Reasoner } from "./src/horn.ts";

const r = new Reasoner();
r.addRule({ id: "r1", premises: ["A"], conclusion: "B" }); // 霍恩规则 A -> B
r.assert("A");        // 断言基础事实,触发正向链
r.knows("B");         // => true
r.explain("B");       // => { depth: 1, baseFacts: ["A"], steps: [...] }
r.retract("A");       // 撤销基础事实,级联清除无独立支持的派生结论
```

## 验收场景

已知 `A` 与 `A->B` 推导出 `B`;再有 `C` 与 `C->B` 后撤销 `A`,
`B` 因 `C` 独立支持而保留;再撤销 `C`,`B` 失去全部支持被安全清除。
完整演示见 `npm run demo`,对应测试见 `test/horn.test.ts` 前三个用例。
