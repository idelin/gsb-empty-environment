# pulse-detector

流式脉冲检测库（TypeScript）：高阈值进入事件、低阈值退出（迟滞），允许合并短暂回落，但限制事件内**累计**间隙时长。

## 语义

- **进入**：样本值 `>= highThreshold` 时开启事件（空闲状态下）。
- **退出/间隙**：样本值 `< lowThreshold` 时进入间隙；`>= lowThreshold` 时恢复（合并回原事件）。
- **累计间隙上限**：一个事件内容忍的间隙总时长不超过 `maxGap`。间隙时长从最后一个不低于低阈值的样本量到恢复样本。超出预算时事件在**该次间隙的起点**（最后一个有效样本处）关闭，间隙段不计入积分；之后重新 `>= highThreshold` 会开启新事件。
- **峰值与积分**：事件保留峰值（及峰值时刻）和梯形积分；被合并的间隙段计入事件积分，被截断的间隙段不计入。
- **未闭合事件**：流结束（`flush()`）时仍打开的事件以 `complete: false` 返回。
- **迟到更正**：`insert(sample)` 支持乱序插入或按相同时间戳替换值，插入后重算受影响的事件（事件 id 不保证跨更正稳定）。

`lowThreshold <= highThreshold`，两者相等时退化为单阈值（`>= T` 进入、`< T` 退出）。构造时校验参数，`push` 要求时间严格递增。

## API

```ts
import { PulseDetector } from './src/pulse-detector.js';

const det = new PulseDetector({ highThreshold: 10, lowThreshold: 5, maxGap: 4 });
det.push({ time: 0, value: 12 });     // 流式输入，时间严格递增
det.insert({ time: 1, value: 2 });    // 迟到/更正样本，触发重算
det.completed;                        // 已闭合事件
det.pending;                          // 当前未闭合事件（complete: false）
const events = det.flush();           // 结束输入，返回全部事件（含未闭合）
```

`PulseEvent` 字段：`id`、`start`、`end`、`peak`、`peakTime`、`integral`、`complete`。

## 构建与测试

本环境离线、无 tsc，因此 `scripts/build.mjs` 用受限但确定性的方式剥离类型标注，生成 `.mjs` 后直接用 Node 运行测试（源码刻意避开无法逐行剥离的 TS 语法）：

```sh
npm test   # 构建并运行 test/pulse-detector.test.ts
```

有网络时可正常 `npm install typescript` 并用 `tsconfig.json` 编译。
