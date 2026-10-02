# exact-pipeline

精确有理数流水线：仿射 + 分段量化，逐步损失审计、双模式舍入对照、完整原像计算、
安全复合简化与整数网格穷举核验。全部数值基于 **BigInt 有理数**，不经过 `Number`
中转大数；`Number` 仅用于循环计数等控制流。

运行环境：Node.js ≥ 23.6（原生类型擦除直接运行 TypeScript，无需编译步骤）。

```bash
npm test          # node --test（Node 内置测试运行器）
npm run sample    # 运行样例 samples/demo.ts
```

## 设计要点

- **精确表示**：`src/rational.ts` 中 `Rat = { n: bigint, d: bigint }`，恒规范化
  （`d > 0`、约分）。`fromString("0.1")` 精确等于 1/10。
- **负零策略**：零永远规范为 `0/1`；BigInt 无 `-0`，`fromString("-0")`、
  负值舍入到零、零乘以负数等所有路径都产出同一个 `0`，两种模式一致。
- **显式舍入规则**（`src/rounding.ts`）：`floor` / `ceil` / `trunc`（向零）/
  `half-up`（平局向 +∞）/ `half-away`（平局远离零）/ `half-even`（银行家舍入）。
  每条规则的**舍入平台**（plateau）端点开闭与平局归属严格对应，例如
  `half-even` 偶数级两端闭、奇数级两端开。
- **操作**（`src/ops.ts`）：
  - `AffineOp`：`y = scale·x + offset`，精确无损；
  - `QuantizeOp`：分段量化，每段独立步长与舍入规则，段域为 `[lo, hi)`，
    末段 `hi` 含端点，保证有界划分无空洞。
- **原像**：`preimageOp` / `pipelinePreimage` 计算输出区间的完整输入原像，
  每个输出层级产生一个平台区间，**平台不合并**，开闭端点精确保留。
- **双模式**（`src/pipeline.ts`）：
  - `stepwise`：每个量化步立即舍入，逐步记录损失；
  - `deferred`：量化步精确透传，末端按 `finalQuant` 统一舍入一次。
  两种模式语义不同、都予保留；`compareModes` 给出差异原因（首个分歧步、
  当场损失、后续仿射放大系数）。
- **累计误差界**：逐步舍入的最大损失经后续仿射 `|scale|` 放大累加，
  保证 `|stepwise输出 − 精确值| ≤ errorBound`（网格上逐点核验）。
- **复合简化**（`src/simplify.ts`）：仅复合相邻仿射
  `a₂(a₁x+b₁)+b₂ = (a₁a₂)x + (a₂b₁+b₂)`；舍入步骤不移动、不合并、不删除、
  不跨步复合（舍入非仿射，移动会改变语义），日志逐步说明。
- **穷举核验**（`src/verify.ts`、`src/verify` 测试）：整数网格上验证
  正逆映射一致（原像 ∩ 网格 ⟺ 映射到该输出的网格点，双向）、误差界覆盖
  实际损失、负零策略，并自动找出逐步/延迟两模式的**最小差异输入**
  （最小 |x|，平局取较小 x）。

## 运行样例（`npm run sample` 实际输出）

```
=== 流水线 ===
  op#0 scale-1.5: affine(scale=1.5, offset=-0.25)
  op#1 round-0.5: quantize([-inf, +inf) step=0.5 half-up)
  op#2 boost-x2: affine(scale=2, offset=1)
  op#3 piecewise-quant: quantize([-inf, 0) step=2 floor | [0, +inf) step=1 half-even)
  累计误差界（stepwise 最坏）: 2.5

=== 1. 逐步执行与每步损失审计（x = 2）===
[stepwise]
  scale-1.5               2 ->     2.75  损失=0  上界=0
  round-0.5            2.75 ->        3  损失=0.25  上界=0.25
  boost-x2                3 ->        7  损失=0  上界=0
  piecewise-quant         7 ->        7  损失=0  上界=0.5
  输出=7  精确值=7  累计误差界=1
[deferred]
  scale-1.5               2 ->     2.75  损失=0  上界=0
  round-0.5            2.75 ->     2.75  损失=0  上界=0  (延迟，未舍入)
  boost-x2             2.75 ->      6.5  损失=0  上界=0
  piecewise-quant       6.5 ->      6.5  损失=0  上界=0  (延迟，未舍入)
  final-round           6.5 ->        6  损失=0.5  上界=0.5
  输出=6  精确值=6.5  累计误差界=0.5

=== 2. 双模式对照与差异原因 ===
  x= 0  stepwise=     1  deferred=     0  不同
    原因: 差异 1：首个分歧点在 round-0.5（quantize([-inf, +inf) step=0.5 half-up)），输入 -0.25 被逐步舍入到 0，当场损失 0.25，经后续仿射放大系数 2；延迟模式保留精确值 0.5 直到末端统一舍入
  x= 1  stepwise=     4  deferred=     4  相同
  x= 2  stepwise=     7  deferred=     6  不同
    原因: 差异 1：首个分歧点在 round-0.5（quantize([-inf, +inf) step=0.5 half-up)），输入 2.75 被逐步舍入到 3，当场损失 0.25，经后续仿射放大系数 2；延迟模式保留精确值 6.5 直到末端统一舍入
  x= 3  stepwise=    10  deferred=    10  相同
  x=-1  stepwise=    -2  deferred=    -2  相同
  x=-3  stepwise=    -8  deferred=    -8  相同

=== 3. 输出区间的完整输入原像 ===
  输出区间 Y = [5, 7)
    原像: [4/3, 5/3)
    原像: [5/3, 2)

=== 4. 复合简化（舍入步骤不移动）===
  原始: op#0:a1 op#1:a2 op#2:a3 op#3:round op#4:a4 op#5:a5
  合并相邻仿射 op#0..op#2 → affine(scale=4, offset=12.5)
  op#3 为舍入步骤（quantize），保持原位不移动
  合并相邻仿射 op#4..op#5 → affine(scale=3, offset=2)
  舍入步骤数量 1 → 1，顺序不变；仅复合相邻仿射
  简化后 op 数: 6 -> 3
    op#0 affine(scale=4, offset=12.5)
    op#1 quantize([-inf, +inf) step=1 half-up)
    op#2 affine(scale=3, offset=2)

=== 5. 整数网格穷举核验（x ∈ [-8, 8]）===
  核验输入数: 17
  失败数: 0
  累计误差界: 1  实际最大偏差: 0.5
  最小差异输入: x=0
    stepwise=1  deferred=0
    原因: 差异 1：首个分歧点在 round-0.5（quantize([-inf, +inf) step=0.5 half-up)），输入 -0.25 被逐步舍入到 0，当场损失 0.25，经后续仿射放大系数 2；延迟模式保留精确值 0.5 直到末端统一舍入
```

## 测试结果（`npm test`）

```
ℹ tests 32
ℹ pass 32
ℹ fail 0
```

覆盖：有理数规范化与负零、六种舍入规则平局行为、平台端点开闭、区间运算、
仿射/分段量化原像、流水线审计与误差界、双模式差异原因、复合简化安全性、
穷举核验与最小差异输入。

## 目录

```
src/rational.ts   BigInt 有理数（规范化、精确十进制解析/输出）
src/rounding.ts   显式舍入规则、量化、平台（plateau）端点
src/interval.ts   开闭端点区间
src/ops.ts        仿射 / 分段量化操作与前向、原像
src/pipeline.ts   双模式执行、逐步审计、误差界、差异原因
src/simplify.ts   相邻仿射复合（不移动舍入步骤）
src/verify.ts     整数网格穷举核验、最小差异输入搜索
samples/demo.ts   运行样例
test/*.test.ts    node:test 测试
```
