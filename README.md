# speaker-gain-interp

定点多扬声器增益插值库。接收整数权重控制点，在控制点之间做定点线性插值，
随后对每帧做恒定功率归一化（Σ gᵢ² = 1），并对总能量误差给出严格上界。

## 特性

- **整数权重控制点**：每个控制点为 `(采样时刻, 每扬声器整数权重)`，权重范围 `[0, maxWeight]`。
- **定点计算**：全部内部运算基于 `bigint` 精确整数运算（Q 格式定点），结果完全确定。
- **误差包围**：`analyzeConfiguration` 对给定配置计算总能量误差 `|Σ gᵢ² − 1|` 的严格上界，
  并与用户给定容差比较。
- **静音处理**：全零权重向量（及第一个控制点之前的采样）输出精确的全零帧，跳过归一化。
- **同刻覆盖**：同一时刻重复添加控制点，后者覆盖前者。
- **跨块一致性**：`processBlock` 以任意块大小流式处理，与 `process` 一次性处理逐位一致
  （每帧输出是 `(采样序号, 控制点, 配置)` 的纯函数）。
- **不可达配置报告**：当精度要求无法满足时，报告给出误差上界、各项误差分解、
  主导误差源及改进建议（如提高 `outFracBits` / `guardBits` / `minGain`）。

## 使用

```ts
import { GainInterpolator, analyzeConfiguration, measureEnergyError } from "speaker-gain-interp";

const config = {
  numSpeakers: 4,
  maxWeight: 1000,
  fracBits: 24,    // 插值增益格式的小数位
  outFracBits: 20, // 输出增益格式的小数位
  guardBits: 16,   // 逆平方根内部的保护位
};

// 精度可行性分析
const report = analyzeConfiguration({ ...config, minGain: 0.02 }, 1e-4);
if (!report.feasible) {
  console.error(report.reasons, report.suggestions);
}

const interp = new GainInterpolator(config);
interp.addControlPoint(0, [1000, 0, 0, 0]);
interp.addControlPoint(48000, [0, 1000, 0, 0]);
interp.addControlPoint(48000, [0, 0, 1000, 0]); // 同刻覆盖，后者生效

// 一次性处理
const frames = interp.process(0, 48000); // GainFrame[]，Q(outFracBits) 整数

// 或流式分块处理（结果与上面逐位一致）
interp.reset();
const block = interp.processBlock(4096);

// 验证某帧的总能量误差
const err = measureEnergyError(frames[100], config.outFracBits);
```

## 误差模型

归一化计算 `S ≈ 2^(fracBits+outFracBits) / √E`（`E = Σ Gᵢ²`），其相对误差上界为
`δ = δ_isqrt + δ_div`，其中：

- `δ_isqrt ≤ 1 / (2^guardBits · √E_min)`，`E_min = (minGain · 2^fracBits)²`
- `δ_div ≤ √N / 2^(outFracBits+1)`

总能量误差上界：

```
|Σ ĝᵢ² − 1| ≤ 2δ + δ² + 2^-outFracBits · √N · (1+δ) + N · 2^(-2·outFracBits) / 4
```

由于单个最低有效位的增益在任何定点格式下都无法精确归一化，该保证仅覆盖
插值能量不低于 `minGain²` 的帧；低于该能量的帧不在保证范围内。

## 构建与测试

```sh
npm install   # 安装 devDependency typescript
npm test      # 编译并运行 test/runTests.ts（39 项断言）
```
