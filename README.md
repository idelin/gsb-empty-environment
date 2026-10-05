# 优惠结算库（coupon-settlement）

组合 **满减券 / 折扣券 / 单品券**，支持券之间的 **互斥组** 与 **先后关系（前置依赖）**，
在所有合法组合中搜索 **实付最少** 者；实付并列时 **用券最少** 者优先。

## 规则约定

- **金额单位**：一律为「分」（整数），API 不接受小数。
- **折扣逐行向下取整**：折扣券对每个适用商品行分别计算
  `floor(行剩余金额 × (100 − percent) / 100)`，不产生跨行进位。
- **结算顺序**：单品券 → 折扣券 → 满减券；同类型内部按 `requires` 拓扑排序。
- **满减门槛**：按结算到该券时的 **实时剩余金额** 判断（即折后价）。
- **互斥组**：`mutexGroup` 相同的券最多选中一张。
- **先后关系**：券的 `requires` 列出的前置券必须同在一个组合中，该券才生效。
- **数量上限**：单笔结算最多 20 张券（`MAX_COUPONS`），超出抛错。
- **退货**：退一件商品后 **重新计算最优组合**，不按比例折算已用券；
  退款额 = 原应付 − 重算后应付。

## 使用

```ts
import { settle, settleAfterReturn, formatSettlement } from './src/index.ts';

const cart = [
  { id: 'shirt', name: '衬衫', unitPrice: 19900, quantity: 1 }, // 单位：分
];

const coupons = [
  { id: 'd1', name: '9折', type: 'discount', percent: 90 },
  { id: 't1', name: '满150减20', type: 'threshold', threshold: 15000, off: 2000,
    requires: ['d1'], mutexGroup: 'g1' },
  { id: 'i1', name: '衬衫单品券', type: 'item', itemId: 'shirt', amountOff: 500 },
];

const s = settle(cart, coupons);
console.log(formatSettlement(s));   // 逐步金额

const r = settleAfterReturn(cart, coupons, 'shirt', 1); // 退货重算
console.log(r.refund);
```

## 券类型

| 类型 | 字段 | 语义 |
| --- | --- | --- |
| `item` 单品券 | `itemId`, `amountOff` | 指定商品每件立减，按数量乘算，不超过行剩余 |
| `discount` 折扣券 | `percent` (1–100) | 折后支付百分比，逐行向下取整 |
| `threshold` 满减券 | `threshold`, `off` | 实时金额达门槛立减 |

通用字段：`mutexGroup`（互斥组）、`requires`（前置券 id 列表）、
`itemIds`（适用商品行，缺省全场）。

## 结果结构

`settle` 返回 `Settlement`：`payable`（最终应付）、`totalDiscount`、
`coupons`（选中券，按结算顺序）、`steps`（逐步金额：每步
`amountBefore / discount / amountAfter` 及逐行 `lineBreakdown`）。

## 实现说明

- 求解：DFS 枚举（≤ 2²⁰），互斥组即时剪枝 +
  以「单券独立优惠上界」做后缀和剪枝；合法性（互斥、依赖）在叶子校验。
- 并列处理：先比 `payable`，再比用券张数，最后按券 id 字典序保证确定性。
- 测试含 60 轮随机对拍（与朴素全枚举比对），验证剪枝不漏最优解。

## 运行

需要 Node ≥ 22.6（利用原生 TypeScript 类型擦除）：

```bash
npm test     # 单元测试 + 随机对拍
npm run demo # 示例：最优组合 + 退货重算
```
