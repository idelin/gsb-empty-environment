# 门店补货规划器（混合整箱配送）

按**整箱**为多家门店规划补货：选择每种箱型的箱数并分配到门店，
最小化 **采购成本 + 运输成本 + 欠供罚金** 的总和。

## 模型

- 最多 **8 种箱型**、**5 家门店**（超出会校验报错）。
- 每种箱型：容量（件/箱）、每箱采购成本、每箱运输成本（可按门店覆盖）。
- 每家门店：需求件数、单位欠供罚金，可选 `maxShortage` 限制“少量欠供”的上限。
- 整箱配送、**禁止拆箱**：送达量只能是箱容量的整数组合，超出需求的部分成为该门店的**剩余库存**。
- **禁止跨店无偿流转**：剩余库存留在原门店，各门店独立优化、独立结算，模型结构上不存在调拨项。

数学形式（对门店 s，箱型 t 的箱数为整数 xₜ ≥ 0）：

```
min  Σₜ (purchaseCostₜ + transportCostₜ,ₛ) · xₜ + penaltyₛ · max(0, Dₛ − Σₜ capacityₜ · xₜ)
s.t. max(0, Dₛ − Σₜ capacityₜ · xₜ) ≤ maxShortageₛ   (若设置)
```

## 算法

箱不可拆且禁止跨店流转 ⇒ 各门店问题相互独立。对每家门店用
**无界背包 DP**（送达量 0 … 需求+最大箱容量，超出该上界必不最优）
求出每个送达量的最小箱成本，再枚举送达量叠加欠供罚金取最小；
回溯得到各箱型箱数。结果精确最优，复杂度 O(箱型数 × 需求上界 × 门店数)。
测试含 200 组随机用例与穷举暴力对拍。

## 使用

```bash
npm run build          # 编译 TypeScript -> dist/
npm start -- examples/sample-input.json        # 人类可读输出
node dist/src/cli.js --json examples/sample-input.json   # JSON 输出
cat input.json | node dist/src/cli.js          # 也支持 stdin
npm test               # 编译并运行测试
```

## 输入格式

```json
{
  "boxTypes": [
    { "id": "L", "capacity": 24, "purchaseCost": 100, "transportCost": 8,
      "transportCostByStore": { "E5": 20 } }
  ],
  "stores": [
    { "id": "E1", "demand": 50, "shortagePenaltyPerUnit": 6, "maxShortage": 4 }
  ]
}
```

- `transportCostByStore`（可选）：按门店覆盖该箱型的运输成本。
- `maxShortage`（可选）：该门店允许的最大欠供件数，缺省不限。

## 输出

每家门店：各箱型箱数、送达件数、欠供件数、**剩余库存**、
采购/运输/罚金分项成本与合计；末尾给出全局汇总（含总剩余库存）。
