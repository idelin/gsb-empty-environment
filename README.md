# gap-tolerant-filter

缺口感知的信号滤波库（TypeScript）。对含缺口的离散信号做有理（ARMA/IIR）滤波：
短缺口线性插值、长缺口断开滤波状态；支持乱序到达的迟到样本，对其做**局部重算**，
并以**版本化输出**说明哪些历史结果因补点而改变。

## 语义

- 样本按整数索引（采样序号）写入：`addSample(index, value)`，允许乱序。
- 相邻真实样本之间缺失的样本数称为**缺口**：
  - 缺口 `≤ gapThreshold`：缺失点按两侧真实样本**线性插值**补齐，滤波状态连续；
  - 缺口 `> gapThreshold`：**断开滤波状态**，后一段从零初始状态重启（缺口内无输出）。
- 滤波器为有理传递函数，系数作为输入：
  `y[n] = ( Σᵢ bᵢ·x[n−i] − Σⱼ≥₁ aⱼ·y[n−j] ) / a₀`（`a₀` 自动归一化）。

## 迟到样本与局部重算

迟到样本可能**缩短缺口**：一个原本断开的长缺口被拆成两个短缺口后，两个滤波段会被
**桥接**为一段，后段不再从零状态重启，其全部历史输出随之失效。

重算始终是局部的，绝不全量回放：

- 每个输出点保存处理完该点后的滤波器**状态快照**；
- 补点到来时，从受影响范围起点前一个真实样本的快照恢复状态，只重算到所在段的段尾；
- 其他分段、以及补点之前的输出完全不动（FIR 滤波下窗口之外的同段输出也保持原版本）。

## 版本化输出

每次 `addSample` 调用产生一个新版本，并返回一个 `ChangeSet`：

- `cause`：`initial` / `append` / `late-sample` / `gap-bridged` / `correction`；
- `affectedRange`：本次局部重算覆盖的网格范围；
- `changes`：逐点的 `added` / `updated` 记录（含旧值、旧版本、新版本、是否插值点）；
- `summary`：中文可读说明，解释哪些历史结果因补点而改变、为什么。

数值未变的输出保留旧版本号；每个索引的完整取值历史可通过 `historyOf(index)` 查询，
`changeSetsSince(v)` 可取回某版本之后的全部变更集。

## API 摘要

```ts
import { GapTolerantFilter } from './src/index.ts';

const f = new GapTolerantFilter({ b: [0.25], a: [1, -0.75], gapThreshold: 2 });

const cs = f.addSample(10, 1.5);   // → ChangeSet（含 summary）
f.getOutput(10);                   // → VersionedOutput | undefined
f.getOutputs();                    // → 全部最新输出（按索引升序）
f.historyOf(10);                   // → 该索引的版本历史
f.getChangeSet(3);                 // → 第 3 版的变更集
f.changeSetsSince(3);              // → 第 3 版之后的变更集
f.segments();                      // → 当前分段视图
```

对同一索引再次 `addSample` 视为**样本修正**（`correction`），从该点局部重算到段尾。

## 运行

源码为仅含可擦除语法的 TypeScript，无需构建：

- Node ≥ 23.6 直接运行：`node examples/demo.ts`、`node --test 'test/*.test.ts'`；
- Node 22.6+ 需加 `--experimental-strip-types`；
- 或用 `tsc` 按 `tsconfig.json` 编译后运行（`npm i -D typescript` 后 `npx tsc`）。

## 复杂度

设段长为 L、滤波器阶数为 p：`addSample` 的重算代价为 O(受影响段尾长度 × p)，
定位样本 O(log n)；每个输出点额外保存 O(p) 的状态快照。
