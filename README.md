# 组合拍卖求解器

接收针对物品组合的报价，求收入最大的无重叠中标集合，并为每个中标者
计算移除其报价后的最优收入（供 VCG 结算使用）。

## 规则

- 每个报价针对一个物品组合（bundle），组合非空且不含重复物品。
- 同一投标方可提交多个报价，彼此互斥（XOR），至多中一个。
- 不同报价的物品组合不得重叠。
- 主目标：中标集合总收入最大。
- 次目标：收入并列时，中标投标编号升序列表的字典序最小。
- 报价数量上限为 25（`MAX_BIDS`）。

## 用法

```ts
import { solveAuction } from "./src/index.ts";

const result = solveAuction([
  { id: "B1", bidder: "alice", items: ["a"], price: 5 },
  { id: "B2", bidder: "bob", items: ["b"], price: 5 },
  { id: "B3", bidder: "carol", items: ["a", "b"], price: 9 },
]);
// result.revenue === 10，中标 B1、B2
// result.settlements[i].revenueWithoutWinner：移除该中标者后的最优收入
// result.settlements[i].vcgPayment：VCG 支付
```

## API

- `solveAuction(bids)`：校验并求解，返回 `{ revenue, winners, settlements }`。
  结算时移除中标投标方的全部报价（同一投标方报价互斥）。
- `validateBids(bids)`：仅校验输入，非法时抛出 `AuctionValidationError`
  （空组合、组合内重复物品、编号重复、价格非法、超过 25 个报价等）。
- `MAX_BIDS`：报价数量上限（25）。

## 实现

分支定界搜索：按价格降序排列报价，以“当前收入 + 剩余报价价格之和”
为上界剪枝；仅在严格小于当前最优时剪枝，收入相等时继续搜索以保证
字典序次目标的正确性。25 个报价的规模下为毫秒级。

## 环境要求与测试

需要 Node.js ≥ 22.6（利用原生 TypeScript 类型擦除直接运行 `.ts`）。

```sh
npm test        # node --experimental-strip-types --test
npm run typecheck  # 需要本地安装 typescript
```

测试覆盖：重叠取舍、组合与单件比较、同一投标方互斥、字典序并列
打破、VCG 结算值、空输入、25 个报价性能，以及重复物品、空组合等
非法输入校验。
