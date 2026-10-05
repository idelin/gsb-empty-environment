# gain-interpolator

多扬声器增益插值库（TypeScript，零依赖，运行于 Bun / Node ≥ 16 的任何 TS 运行时）。

- **整数权重控制点**：`addControlPoint(time, weights)`，权重为整数；同一时刻重复添加会**覆盖**旧值。
- **定点插值与归一化**：控制点间线性插值，全部以 Q(F) 定点（BigInt 精确舍入）计算；每样本归一化到单位能量（Σg² = 1）。
- **零权重向量 = 静音**：插值结果为零向量时输出全零增益，`muted = true`，目标能量为 0。
- **误差包围**：每帧携带经区间推理认证的**总能量误差上界** `energyErrorBound`（Q(2F)），保证 `|Σg² − 目标能量| ≤ 上界`。
- **跨块一致**：每帧仅由绝对采样时刻决定，任意分块处理与一次处理**逐位相同**。
- **不可达配置报告**：`checkConfig`（静态）与 `analyze`（沿轨迹扫描）报告无法满足给定能量误差界的配置与违例样本。

## 使用

```ts
import { GainInterpolator } from "./src/index";

const gi = new GainInterpolator({ speakers: 4, fracBits: 32 });
gi.addControlPoint(0,     [10, 0, 0, 0]);
gi.addControlPoint(48000, [6, 8, 0, 0]);
gi.addControlPoint(96000, [0, 0, 0, 0]);   // 静音

const frames = gi.processBlock(0, 96000);   // 任意分块结果一致
const report = gi.analyze(0, 96000, 1e-6);  // 验证总能量误差界
if (!report.feasible) console.log(report.reasons, report.violations);
```

## API

| 接口 | 说明 |
| --- | --- |
| `new GainInterpolator({ speakers, fracBits? })` | `fracBits` 默认 32，范围 [8, 53] |
| `addControlPoint(time, weights)` | 整数时刻、整数权重；同刻覆盖 |
| `frameAt(time)` / `processBlock(start, length)` | 单帧 / 块处理，返回 `Frame[]` |
| `analyze(start, length, energyErrorBound)` | 扫描区间，返回 `AnalysisReport`（`feasible`、`maxEnergyErrorBound`、`violations`、`reasons`） |
| `GainInterpolator.checkConfig({ speakers, fracBits, energyErrorBound })` | 静态可行性检查，给出 `requiredFracBits` |
| `GainInterpolator.requiredFracBits(speakers, bound)` | 稳态误差模型 `2√n·2^-F ≤ bound` 的最小 F |

## 误差模型

每帧 `energyErrorBound` 由两部分舍入误差严格包围：末位除法舍入（≤ 1 ulp）与整数开方下取整
（`|w_k|·2^F·(s−r²)/(2r³)`，其中 `s = Σw²`，`r = ⌊√s⌋`）。注意：接近零的权重向量（如从静音淡入的起始段）
归一化是病态的，增大 `fracBits` 也无法消除——`analyze` 会把这些样本列入 `violations`。

## 运行

```sh
bun test          # 11 个测试：插值、覆盖、静音、误差包围、跨块一致、可行性报告
bun examples/demo.ts
```
