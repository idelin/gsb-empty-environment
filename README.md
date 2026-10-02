# 双时态价格规则引擎（Bitemporal Price Ledger）

同时按 **业务生效时间（Valid Time）** 与 **系统知悉时间（Transaction Time）** 查询的价格账本。
纯 TypeScript 实现，零依赖，使用 Node.js 内置测试运行器（`node:test`）验证。

## 语义

- 价格区间为**左闭右开**：`[validFrom, validTo)`。
- 业务时间重叠的规则按**知悉时间后入为主**：新规则登记时，旧规则当前开放的
  知悉区间在新规则的 `txTime` 处关闭，未重叠的残余部分以新知悉区间重新开放。
- **撤销只影响撤销发生之后的知悉时间查询**：`revoke` 把规则开放的知悉区间在
  撤销时刻关闭，历史知悉视角下的查询结果不变，且不会复活被覆盖的旧价格。
- 金额一律使用**整数分**（`priceCents`）。
- 知悉时间单调不回退（账本只允许追加新知悉）。

## 环境要求

Node.js >= 22.6（利用原生 TypeScript 类型擦除直接运行 `.ts`，无需编译）。
仓库内附 `.nvmrc`（v23.9.0）。

## 运行

```sh
npm test    # 运行 node:test 测试
npm run demo  # 运行验收场景示例
```

## API

```ts
import { BitemporalPriceLedger } from './src/price-ledger.ts';

const ledger = new BitemporalPriceLedger();
const id = ledger.insert(10, 0, 20, 2);   // 10 分，业务 [0,20)，知悉于 2
ledger.insert(15, 5, 10, 8);              // 15 分，业务 [5,10)，知悉于 8

ledger.priceAt(7, 7);   // 10
ledger.priceAt(7, 8);   // 15（后入为主）
ledger.priceAt(10, 8);  // 10（[5,10) 右开）

ledger.revoke(id, 12);  // 撤销只影响知悉时间 >= 12 的查询
ledger.history();       // 全部历史切片（审计）
```

## 文件结构

- `src/price-ledger.ts` — 引擎实现
- `test/price-ledger.test.ts` — `node:test` 测试（含验收边界）
- `examples/demo.ts` — 验收场景运行样例
