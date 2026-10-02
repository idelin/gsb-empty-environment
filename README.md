# HTB 层次化令牌桶带宽分配器

TypeScript 实现的层次化令牌桶（Hierarchical Token Bucket）树状带宽分配器，
仅依赖 Node.js 内置模块（`node:test` + 类型擦除运行 TS，Node >= 22.6）。

## 模型

- 树中每个节点拥有：保证速率 `rate`、上限 `ceil`、借用权重 `weight`。
- 叶子节点代表业务线，内部节点代表部门，根节点为总带宽。
- 每个 tick 叶子发布需求 `demand`，分配器自底向上聚合需求、自顶向下
  分层水填（water-filling）计算目标配额：
  1. 先满足各节点的保证速率 `rate`；
  2. 兄弟间空闲配额按 `weight` 等比例借出，受 `ceil` 与需求封顶；
  3. 保证速率总额超售时，按 `weight` 等比例抢回。
- 应用配额平滑逼近目标：回收按 `reclaimAlpha` 每 tick 收敛，扩容只能
  使用已实际释放的容量，因此任意 tick 根预算都不会被突破。

## 运行

```bash
# 需要 Node >= 22.6（使用内置测试运行器与 --experimental-strip-types）
npm test    # node:test 测试
npm run demo  # 验收场景演示
```

## 验收场景（examples/demo.ts）

根 100MB；部门 A 保证 40MB，部门 B 保证 60MB、上限 90MB：

- 阶段一：A 只用 10MB → B 借得 A 空闲的 30MB，达到 90MB（ceil 封顶）。
- 阶段二：A 恢复至 40MB → B 被平滑压回 60MB（90 → 75 → 67.5 → … → 60），
  每个 tick 总量恒为 100MB，B 始终不低于保证值 60MB。

## API

```ts
import { HtbAllocator } from "./src/htb.ts";

const htb = new HtbAllocator(
  {
    name: "root", rate: 100,
    children: [
      { name: "A", rate: 40 },
      { name: "B", rate: 60, ceil: 90 },
    ],
  },
  { reclaimAlpha: 0.5 }, // 可选：回收/扩容平滑系数
);

htb.setDemand(["A"], 10);
htb.setDemand(["B"], 100);
const alloc = htb.tick(); // Map { "A" => 10, "B" => 90 }
```

多层级部门直接在 `children` 中嵌套即可，叶子路径如 `["deptA", "a1"]`。

## 测试覆盖（test/htb.test.ts，8 项全部通过）

1. A 空闲配额借给 B 至 ceil（90MB）
2. 空闲再多 B 也不超过 ceil
3. A 恢复时 B 平滑单调压回 60MB，且每 tick 总量 ≤ 100MB
4. 保证速率优先于借用
5. 空闲带宽按兄弟权重等比例分配（1:1:2）
6. 保证速率超售时按权重等比例抢回
7. 多层级：借用受部门配额与 ceil 约束
8. 输入校验
