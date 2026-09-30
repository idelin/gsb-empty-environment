# pipeline-divergence-analyzer

精确比较两条数值计算流水，定位**首个**舍入 / 单位 / 分母口径分歧，保留输入与中间精确值，给出可复算解释。

## 运行要求

- Node.js ≥ 22.6（推荐 ≥ 23，可直接运行 TypeScript；无需任何 npm 依赖，零安装）。

```bash
node src/sample.ts        # 运行 5 个场景样例
node --test test/*.test.ts  # 运行 Node.js 内置测试（29 个用例）
```

## 设计

- **精确算术**（`src/rational.ts`）：所有数值为 BigInt 分子/分母的 `Rat`，绝不经过 `Number` 中转大数（不安全的 `Number` 输入会被显式拒绝）。舍入规则显式：`half-up / half-even / half-down / down / up / floor / ceil`，`roundRat(x, scale, rule)` 逐条可复算。
- **单位系统**（`src/units.ts`）：单位 = 维度指数表 + 精确换算因子。维度不同是硬性错误，任何情况下不用容差掩盖；同维度不同因子（如 m vs cm）按单位口径分歧报告。
- **计算 DAG**（`src/dag.ts`）：节点为 const / input / add / sub / mul / div / round / convert。求值产生完整 Trace：每个节点保留精确值、单位与所应用规则的描述。节点可标 `traced:false` 表示该流水未记录此中间证据。
- **DAG 对齐**（`src/align.ts`）：先按来源规范化键对齐（加法/乘法做交换律+结合律展平，等价中间步骤自动对齐），再对未匹配节点做位置回退（同 kind、父节点互相对应），因此能发现"同一位置、不同规则"的分歧，而不是简单比较第 N 个节点。
- **分歧定位**（`src/compare.ts`）：精确比较已对齐节点对（无容差），找出"所有祖先都一致的最浅分歧节点对"作为原点，分类为舍入规则 / 单位换算 / 分母口径 / 常量 / 单位维度 / 结构分歧，并给出从原点延伸到输出的精确差异链。输出不同但无可比对中间证据 → `unlocatable`；最终相同但中间不同 → 记录在 `intermediateOnly`。
- **反事实确认**（`src/counterfactual.ts`）：把 A 中疑似致因的规则（舍入规则 / 换算因子 / 分母常量）替换为 B 的规则，独立重算 A′，与 B 的输出精确比对（值+单位）。先试单点，再逐步扩大子集，给出最小已确认规则子集；未确认时报告精确残差。
- **最小反例搜索**（`src/search.ts`）：在有限有理输入域内按复杂度（|分子|+分母）递增枚举，找到最小反例并输出完整精确差异链；预算耗尽报告 `budget-exhausted`（未决），空间耗尽报告 `exhausted-space`。定义域外输入（如除零）自动跳过。

## 样例场景（`src/sample.ts`）

1. 舍入规则分歧：`1/8=0.125` 保留 2 位小数，half-up → 0.13 vs half-even → 0.12；反事实替换确认；自动搜索到最小反例 `total=1/2, qty=4`；预算不足时输出未决状态。
2. 单位口径错误：B 漏掉 m→cm 换算，判定 `unit-mismatch`，不容差掩盖。
3. 最终相同但中间不同：折扣 0.045 → 0.05 vs 0.04，取整后同为 10，中间分歧被记录。
4. 分母口径分歧：均值 `/n` vs `/(n-1)`，原点分类为 `denominator-convention`，反事实替换常量确认。
5. 缺少中间证据：B 的中间节点未记录，输出不同 → 标记 `unlocatable` 而非猜测。
