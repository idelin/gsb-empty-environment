# pulse-detector

流式脉冲检测库：高阈值进入事件、低阈值退出（迟滞），允许合并短暂回落，但限制单次与累计间隙时长。

## 语义

- 采样值 `>= highThreshold` 时开启事件；开启后跌破 `lowThreshold` 进入"间隙"状态。
- 间隙内重新 `>= highThreshold` 且间隙时长 `<= maxGap`、累计间隙 `<= maxTotalGap` 时，回落被合并进原事件；否则事件在间隙起点处闭合。
- 事件保留峰值（`peak`/`peakTime`）、梯形积分（`integral`）与累计间隙（`totalGap`）。
- 流末尾仍未闭合的事件由 `flush()` 以 `complete: false` 标出。
- `correct(t, v)` 支持迟到/修正数据（替换或插入样本），重算后按事件区间 `(start, end)` 报告被撤回与新产生的事件——修正可能把原事件拆成多个。

## 用法

```ts
import { PulseDetector } from './src/pulse-detector';

const d = new PulseDetector({
  highThreshold: 10,
  lowThreshold: 5,
  maxGap: 2,       // 单次回落最多桥接 2 个时间单位
  maxTotalGap: 3,  // 单事件累计桥接不超过 3
});

for (const [t, v] of stream) {
  const finalized = d.feed(t, v);   // 本样本闭合的事件（通常为空）
}
const tail = d.flush();             // 末尾未闭合事件，complete=false

// 迟到更正
const { retracted, emitted } = d.correct(4, 1);
```

## 脚本

- `npm run build` — 编译到 `dist/`
- `npm test` — 编译并运行测试（覆盖阈值相等、连续间隙累计上限、迟到更正拆分等场景）
