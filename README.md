# coupon-settlement

优惠结算库：组合**满减券、折扣券、单品券**，支持券之间的**互斥组**与**先后依赖**，
枚举所有合法组合并求**实付最少**的方案。所有金额均为整数，单位**分**。

## 规则语义

- **结算顺序**：单品券 → 折扣券 → 满减券；显式 `dependsOn` 依赖优先于该默认顺序
  （全局拓扑序，同层按券 id 排序）。依赖成环直接报错。
- **单品券** `ITEM`：指定 SKU 每件立减 `amountOffPerUnit` 分，整行优惠不超过该行剩余金额。
- **折扣券** `DISCOUNT`：`ratePercent` 表示折后百分比（85 = 85 折），
  **逐行向下取整**：`折后 = floor(行金额 × ratePercent / 100)`；可用 `skuIds` 限定范围。
- **满减券** `THRESHOLD`：结算到本券时，以**当前应付金额**判断是否达到 `threshold`，
  达到则减 `amountOff`。
- **互斥组** `mutexGroup`：同组券两两不可同时使用。
- **先后关系** `dependsOn`：列出的券必须也被选中且先于本券结算，否则含本券的组合非法。
- **合法性**：选中的每张券都必须真实生效（有适用商品、产生优惠、门槛达标），
  否则整个组合非法。
- **最优解**：实付最少；并列时**用券最少**；再并列取券 id 字典序最小者（结果确定）。
- **规模**：最多 20 张券（2^20 子集枚举，含掩码预检，最坏情况约秒级）。
- **退款**：退一件商品后从购物车移除并**整体重新计算**最优组合，不按比例退券。

## 用法

```ts
import { settle, refundItem, CartLine, Coupon } from 'coupon-settlement';

const cart: CartLine[] = [
  { skuId: 'A', unitPrice: 1000, quantity: 2 }, // 单位：分
  { skuId: 'B', unitPrice: 8000, quantity: 1 },
];

const coupons: Coupon[] = [
  { id: 'I1', type: 'ITEM', skuId: 'A', amountOffPerUnit: 100 },
  { id: 'D1', type: 'DISCOUNT', ratePercent: 50 },
  { id: 'T1', type: 'THRESHOLD', threshold: 4000, amountOff: 900 },
  { id: 'T2', type: 'THRESHOLD', threshold: 3000, amountOff: 500, mutexGroup: 'g', dependsOn: ['D1'] },
];

const s = settle(cart, coupons);
// s.payable        最优实付（分）
// s.couponIds      选中的券（按结算顺序）
// s.steps          逐步金额：每步 amountBefore / discount / amountAfter 及逐行明细

const r = refundItem(cart, coupons, 'A', 1);
// r.refundAmount   应退金额 = 原实付 - 重算后实付
// r.after          退货后重新计算的最优结算
```

## 脚本

- `npm run build`：编译到 `dist/`
- `npm test`：编译并运行测试（`test/run-tests.ts`）
