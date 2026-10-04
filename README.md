# gap-aware-filter

缺口感知的信号滤波库（TypeScript，零依赖）。

## 语义

- **有理滤波器**：`H(z) = B(z) / A(z)`，系数 `b`、`a` 作为输入，内部归一化到 `a[0] = 1`，
  采用直接 II 型转置实现，状态在样本流上递推。
- **短缺口**（相邻样本间隔 `<= maxInterpolationGap`）：按标称 `sampleInterval` 在线性插值补点，
  补点与真实样本一起进入滤波器，输出中标记 `interpolated: true`。
- **长缺口**（间隔 `> maxInterpolationGap`）：断开滤波状态，下一段从零状态重启，缺口内不产生输出。
- **迟到样本**：样本允许乱序到达。迟到样本会缩短缺口，甚至把长缺口补成短缺口（两段合并，
  报告中 `bridgedGap: true`）。同时间戳重复写入视为对已有样本的修正。

## 局部重算与版本化

长缺口会重置滤波状态，因此各段相互独立：补点只影响它所在的段，重算范围被限制在
`affectedRange` 内，不会跨越长缺口波及更早的历史。

- 每次 `addSample` 使全局版本号 +1；每个输出点携带其最后被写入时的版本号。
- 重算后逐点对比新旧切片：值未变的点**保留原版本号**；新增/更新/删除的点记入
  `RecomputeReport.changes`，精确说明哪些历史结果因本次补点而改变。
- `formatReport(report)` 生成人类可读的变化说明。

## 用法

```ts
import { GapAwareFilter, formatReport } from './src/gap-aware-filter.ts';

const filter = new GapAwareFilter({
  b: [0.5, 0.3, 0.2],      // 分子系数
  a: [1, -0.4],            // 分母系数
  sampleInterval: 1,       // 标称采样间隔
  maxInterpolationGap: 3,  // 缺口阈值
});

filter.addSample(0, 1);
filter.addSample(1, 2);
filter.addSample(10, 5);   // 长缺口：状态断开

const report = filter.addSample(4, 3); // 迟到样本：缩短缺口
console.log(formatReport(report));     // 哪些历史结果被改变
console.log(filter.getOutputs());      // 逐点版本化输出
```

完整可运行示例见 `demo.ts`（`node demo.ts`，需 Node >= 22.6）。

## 脚本

- `npm test`：运行测试（Node >= 22.6 可直接执行 TypeScript；本仓库用 Node 23 验证）
- `npm run typecheck`：`tsc --noEmit` 类型检查
