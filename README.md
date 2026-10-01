# 双时态合同履约状态机

以两个时间维度跟踪合同履约：

- **业务时间**：合同条款与交付在业务上生效的时间轴，合同业务期为 `[validFrom, validTo)`
- **知悉时间**：系统获知并录入事实的时间轴，只增不减（`advanceKnowledgeTime`）

## 状态机

合同状态沿业务时间轴演进，由双时态切片 `(业务时间, 知悉时间)` 实时推导：

| 状态 | 条件 |
| --- | --- |
| 草案 `DRAFT` | 业务时间 < 合同期起点 |
| 生效 `EFFECTIVE` | 进入合同期，但该知悉时间下尚无可见交付 |
| 履行中 `PERFORMING` | 该知悉时间下存在业务时间 ≤ 切片的可见交付 |
| 终止 `TERMINATED` | 业务时间 ≥ 合同期终点 |

## 交付凭证的补交与作废

每份交付凭证携带知悉区间 `[txFrom, txTo)`：

- `recordDelivery(businessTime, quantity)` 在当前知悉时间录入，支持补交历史业务时间的交付
- `voidDelivery(id)` 在当前知悉时间关闭区间（`txTo = 当前知悉时间`），历史切片仍可重现
- 切片 `(bt, kt)` 下可见的凭证满足 `txFrom <= kt < txTo` 且 `businessTime <= bt`

完成度 = 可见交付数量之和 / 要求交付总量 × 100，封顶 100%。

## 运行

要求 Node.js ≥ 23.6（内置 TypeScript 类型擦除，零依赖）。

```sh
npm test    # node --test（Node 内置测试运行器）
npm run demo
```
