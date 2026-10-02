# rete-matcher

增量 RETE 网络模式匹配器（TypeScript，零依赖，Node.js 内置测试）。

## 核心能力

- **Alpha 网络**：按事实类型 + 常量测试（`eq/ne/lt/lte/gt/gte`）过滤，相同条件的 Alpha Memory 自动共享。
- **Beta 网络**：Join 节点基于共享变量做跨事实 Join，Token 沿网络增量传播。
- **增量激活**：`addFact` 只沿网络传播新 WME，绝不全量重扫事实库。
- **负 Token 撤回**：`retractFact` 生成 `NegativeToken`，沿正 Token 的精确路径级联湮灭子孙 Token，精准清除冲突集中的待激活条目。

## 运行要求

Node.js >= 22.6（使用原生类型擦除直接运行 TypeScript，无需编译）。

```bash
npm test        # node --test（内置测试运行器）
npm run demo    # 运行验收样例
```

## 用法

```ts
import { ReteNetwork } from "./src/index.ts";

const network = new ReteNetwork();

network.addRule({
  name: "adult-with-big-order",
  conditions: [
    { type: "User",  where: { age: { gte: 18 } },  bind: { name: "?user" } },
    { type: "Order", where: { amount: { gt: 50 } }, bind: { user: "?user", id: "?orderId" } },
  ],
});

network.addFact({ type: "User",  attrs: { name: "alice", age: 25 } });
const orderId = network.addFact({ type: "Order", attrs: { id: "O-1", user: "alice", amount: 100 } });

network.getConflictSet(); // => 1 个待激活条目
network.retractFact(orderId);
network.getConflictSet(); // => 0，负 Token 精准清除
```

## 结构

- `src/types.ts` — Fact / WME / Condition / Activation 等类型
- `src/token.ts` — 正 Token（父子树）与负 Token（级联湮灭）
- `src/network.ts` — AlphaMemory、JoinNode、BetaMemory、ProductionNode 与 `ReteNetwork`
- `test/rete.test.ts` — 验收边界 + Alpha 过滤 / Join / 撤回 / 增量行为共 9 个用例
- `examples/demo.ts` — 验收样例脚本
