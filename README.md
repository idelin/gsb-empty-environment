# 拆单求解器（order-splitting-solver）

为订单选择发货仓并确定各仓发货量。每个仓有**启用运费**（固定）、**单件运费**、
**各 SKU 库存**，可选**免运门槛**；同种商品允许拆分到多个仓发货。

- **主目标**：总运费最低
- **次目标**：包裹数（发货仓数量）最少
- **再次目标**：总发货件数最少（抑制不必要的超发）
- 输出**最优性下界**（lower bound）与 gap，作为最优性证明
- 支持**免运门槛**：仓内发货件数 ≥ 门槛时该仓全部运费免除，因此可能出现
  "多取一件反而更便宜"（超发凑门槛）的最优解

约束规模：最多 8 仓、12 种商品。

## 费用模型

对仓 `w`，设其发货总件数为 `x`：

```
cost_w(x) = 0                                   , x = 0
cost_w(x) = fixedFee_w + perItemFee_w * x       , 1 <= x < threshold_w
cost_w(x) = 0                                   , x >= threshold_w （免运）
```

约束：每个 SKU 发货总量 ≥ 订单需求（默认允许超发，`allowOverDelivery: false`
时严格等于需求）；各仓各 SKU 发货量不超过库存。

## 算法

精确求解，分两层：

1. **枚举仓集合 × 模式**（共 `3^8 = 6561` 种组合）：每个仓要么不用、要么
   普通模式（`1 <= x <= threshold-1`）、要么免运模式（`x >= threshold`）。
   每个组合归结为**带流量下界的最小费用流**（仓→SKU 边容量为库存，SKU→汇
   边下界为需求量，仓边下界/上界刻画模式），用 SSP + 势能求解。流量为整数，
   故结果为精确最优解。
2. **最优性下界**：把每仓运费松弛为下界函数（无门槛仓保持原费用，有门槛仓
   松弛为 0），枚举无门槛仓的启用子集将固定费用精确化，每个子集是一个运输
   问题。`lowerBound <= OPT` 恒成立；无门槛时 `lowerBound == OPT`。

复杂度与规模无关项均为常数：满规模（8 仓 × 12 SKU）实测约 0.1 秒。

## 使用

需要 Node.js ≥ 23.6（或 ≥ 22.6 加 `--experimental-strip-types`），
直接运行 TypeScript，无需编译与依赖安装。

```bash
# 命令行：从文件或 stdin 读入订单 JSON
node src/cli.ts examples/sample.json
node src/cli.ts examples/sample.json --exact   # 禁止超发（严格按需求发货）
cat examples/sample.json | node src/cli.ts

# 测试
node --test 'test/*.test.ts'
```

### 输入

```json
{
  "items": [{ "sku": "A", "quantity": 4 }],
  "warehouses": [{
    "id": "华南仓",
    "fixedFee": 10,
    "perItemFee": 1,
    "freeShippingThreshold": 7,
    "stock": { "A": 10 }
  }],
  "options": { "allowOverDelivery": true }
}
```

### 输出

```json
{
  "status": "optimal",
  "totalCost": 0,
  "packages": 1,
  "lowerBound": 0,
  "gap": 0,
  "allocations": [{
    "warehouseId": "华南仓",
    "items": { "A": 5 },
    "totalItems": 5,
    "shippingCost": 0,
    "freeShippingApplied": true
  }],
  "overDelivery": { "A": 1 }
}
```

- `lowerBound` / `gap`：最优性下界及其与最优值的差距（`gap = 0` 即下界已收紧）
- `overDelivery`：为凑免运门槛而超发的数量（`allowOverDelivery: false` 时恒为空）
- 库存总和不足时返回 `{ "status": "infeasible", ... }`

### 作为库调用

```ts
import { solve } from './src/solver.ts';

const result = solve(items, warehouses, { allowOverDelivery: true });
```

## 目录结构

- `src/types.ts` — 输入输出类型
- `src/minCostFlow.ts` — 带边下界的最小费用流
- `src/solver.ts` — 枚举 + 最小费用流的精确求解器与下界计算
- `src/cli.ts` — 命令行入口
- `test/solver.test.ts` — 单元测试、随机实例与暴力枚举对拍、满规模性能冒烟
- `examples/sample.json` — 示例（演示超发凑免运门槛）
