# HTB 层次化令牌桶带宽分配器

TypeScript 实现的层次化令牌桶（Hierarchical Token Bucket）树状带宽分配器，
仅依赖 Node.js 内置模块（`node:test` 做测试），无任何第三方依赖。

## 模型

- **树状组织**：内部节点代表部门，叶子节点代表业务线。
- **rate / ceil**：每个节点有保证带宽 `rate`（MB/s，同时作为抢回权重）和上限
  `ceil`（默认继承父节点）。
- **借用**：兄弟节点空闲的保证带宽可被借用（沿树递归向上），不超过 `ceil`。
- **按权重等比例抢回**：总需求超过父节点容量时，按各子节点 `rate` 加权
  水填充（weighted water-filling）等比例分配。
- **平滑压制**：强制执行的配额通过 EWMA 向公平份额目标收敛（升配 `attackTau`
  默认 0.2s，降配 `releaseTau` 默认 1.0s），避免瞬时掐断借用方。
- **令牌桶**：每个叶子持有令牌桶（深度 = `ceil × burstSeconds`），按当前平滑
  配额速率补充，用于实际放行流量。

## 环境要求

Node.js >= 22.6（直接运行 TypeScript，无需编译；推荐 v23，见 `.nvmrc`）。

```sh
nvm use          # 或任意 >= 22.6 的 Node
npm test         # node --test，11 个用例
npm run demo     # 验收场景运行样例
```

## 用法

```ts
import { HtbAllocator } from "./src/index.ts";

const htb = new HtbAllocator({
  name: "root",
  rate: 100, // 根节点 100MB/s
  children: [
    { name: "deptA", rate: 40, children: [{ name: "bizA", rate: 40 }] },
    { name: "deptB", rate: 60, children: [{ name: "bizB", rate: 60 }] },
  ],
});

htb.setDemand("bizA", 10);   // A 只用 10MB/s
htb.setDemand("bizB", 100);  // B 需求拉满
htb.tick(0.1);               // 推进仿真时钟
htb.allocOf("bizB");         // -> 平滑上升至 90（借用 A 空闲的 30）
htb.consume("bizB", 5);      // 从 B 的令牌桶放行 5MB
```

## 验收结果

`npm run demo` 输出（截取关键行）：

```
  t(s) | demandA allocA | demandB allocB |  total
   1.4 |   10.0   10.0 |  100.0   90.0 |  100.0   <- A 空闲，B 借用到 90
   2.0 |   10.0   10.0 |  100.0   90.0 |  100.0   <- A 流量恢复
   2.2 |   40.0   29.0 |  100.0   84.6 |  113.5   <- 平滑过渡，非瞬时掐断
   4.0 |   40.0   40.0 |  100.0   64.1 |  104.1
   8.0 |   40.0   40.0 |  100.0   60.1 |  100.1   <- B 收敛回保证值 60
```

- A 仅用 10MB/s 时，B 突破 60 最高借用至 90（60 保证 + 30 空闲）。
- A 恢复 40MB/s 后，B 按 EWMA（tau=1s）单调平滑压回 60，无下冲。
- 过渡期间总量短暂超过 100 是平滑抢回的固有特性（A 升配快、B 降配慢），
  稳态严格收敛于 40/60。

`npm test`：11 个用例全部通过，覆盖借用、跨部门借用、按权重等比例抢回、
ceil 封顶、平滑收敛单调性、令牌桶限速与参数校验。

## 文件结构

- `src/htb.ts` — 核心实现（树、加权水填充、EWMA 平滑、令牌桶）
- `src/index.ts` — 导出入口
- `test/htb.test.ts` — `node:test` 测试
- `examples/demo.ts` — 验收场景运行样例
