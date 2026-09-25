# interval-policy-engine

把整数时间区间规则编译为**不重叠判定段**（decision segments）的 TypeScript 库。
零依赖，使用 Node.js 内置测试运行器（Node >= 22.6，依赖原生 type-stripping 直接运行 `.ts`）。

## 特性

- **段编译**：规则按半开区间 `[start, end)`（整数格）编译为不重叠段；相邻且规则集相同的段合并。
- **共享决策结构**：每段引用按规则集规范化的不可变 `DecisionNode`，规则集相同的段共享同一节点对象，
  不做"时间 × 属性"笛卡尔展开。
- **增量更新**：`insert` / `remove` / `upsert` / `applyBatch`；批量操作先整体校验再原子提交，
  结果与来源（source）同时更新；区间端点改变通过 upsert 撤回旧证据（report 中体现 `retracted`）。
- **规则模型**：多条件维度（有限枚举集合、整数比较、and/or/not 布尔运算）、优先级（高者优先）、
  有效版本（低版本 upsert 被拒绝为 stale）。
- **查询取证**：`query(t, attrs)` 返回决策、来源规则（id+version）、所在段与决策节点 id；
  无规则命中时回落到默认策略（`defaultDecision`）。
- **只读显式属性**：条件只读取查询时显式传入的属性，缺失属性按 false 处理，不接外部身份服务。
- **更新报告**：每次更新记录实际触及的段与决策节点、新增/撤回的证据区间。
- **对照验证**：`verifyAgainstBruteForce` 在有限时间格 × 属性域全枚举上与朴素逐点扫描比对。

## 运行

```sh
npm test     # node --test test/*.test.ts
npm run demo # node examples/demo.ts
```

## 示例

```ts
import { PolicyEngine, ALWAYS, and, eq, intCmp, not } from './src/index.ts';

const engine = new PolicyEngine({
  schema: {
    role:  { kind: 'enum', values: ['admin', 'user', 'guest'] },
    level: { kind: 'int', min: 0, max: 5 },
  },
  defaultDecision: 'deny',
});

engine.insert({ id: 'a', version: 1, start: 0, end: 5, decision: 'allow', priority: 0, condition: ALWAYS });
engine.insert({ id: 'b', version: 1, start: 5, end: 10, decision: 'deny',  priority: 0, condition: ALWAYS });

engine.query(5).decision;            // 'deny'（半开边界：5 属于 [5,10)）
engine.query(4).source?.ruleId;      // 'a'
engine.remove('a');
engine.query(4).defaultApplied;      // true（撤销后恢复默认策略）
```

## 文件结构

- `src/conditions.ts` — 条件 AST、求值、属性域 schema、全枚举器
- `src/engine.ts` — 段引擎：增量插删、原子批量、共享决策节点、更新报告
- `src/verify.ts` — 暴力对照验证器
- `test/engine.test.ts` — node:test 测试（12 例）
- `examples/demo.ts` — 可运行样例
