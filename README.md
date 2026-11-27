# promql-extrapolated-rate

一个零依赖、可直接导入的 TypeScript 包，按 Prometheus `extrapolatedRate` /
`instantValue` 规范（含 `1.1 * avgInterval` 门限的世代）实现 PromQL 的
`rate()`、`increase()` 与 `irate()`。

输入是按时间升序排列的样本（毫秒时间戳 `t` + IEEE-754 `float64` 值）、求值
时间戳 `evalTs` 与窗口长度 `rangeMs`。

## 为什么不能用朴素首尾差值

- 计数器会发生多次 **重置**：当 `v[i] < v[i-1]` 时累加 `v[i]` 而不是负差值。
- 首/尾样本通常不贴在窗口边界，需要按平均采样间隔向两端 **外推**。
- 新进程的计数器从 0 附近起步，向左边界回推可能 **穿过零点** 得到负计数，
  必须把左端外推时长钳位到零点。
- 稀疏采样下不能按完整空隙外推，否则会被过度放大，需要 `1.1 * avgInterval`
  门限与半个平均间隔的截断。
- Prometheus 的陈旧标记 `StaleNaN`（特定位模式 `0x7ff0000000000002`）与普通
  数学 `NaN` 语义不同，必须按位区分。

## 安装与导入

```ts
import {
  rate,
  increase,
  irate,
  isStaleNaN,
  decodeSamples,
  decodeColumnarSamples,
  STALE_NAN_BITS,
  type Sample,
} from './src/index.js';
```

## API

### `rate(samples, evalTs, rangeMs, isCounter = true): number | undefined`

返回窗口内每秒平均增量（带边界外推）。`isCounter=false` 时按 gauge 处理
（保留负差值、不做零点钳位）。少于两个有效非陈旧样本时返回 `undefined`
（该序列不输出）；存活样本中存在普通 `NaN` 时返回 `NaN`。

### `increase(samples, evalTs, rangeMs, isCounter = true): number | undefined`

返回窗口内的总增量。它与 `rate` 共用同一条外推后的平均速率，再乘以外推持续
时间（`rangeSec + (dStart + dEnd) - avgIntervalSec`）。返回值约定同 `rate`。

### `irate(samples, evalTs, rangeMs, isCounter = true): number | undefined`

仅取窗口内 **最后两个** 非陈旧样本，按重置规则计算瞬时速率，不做任何外推。

### `isStaleNaN(value: number): boolean`

通过 `DataView` 读取原始 IEEE-754 位，判断是否恰好为 `0x7ff0000000000002`。
普通 `NaN`（任何其它位模式）返回 `false`。

> 注意：V8 在经 `Float64Array` 存取时会规范化 `NaN` 的 payload，因此位级识别
> 必须使用 `DataView` / `BigInt64Array` 的整数视图，本包的解码函数即按此实现。

### `decodeSamples(buffer, options): Sample[]`

解码交错二进制缓冲：每个样本占 `strideBytes`（默认 16），依次为小端 int64
毫秒时间戳与 float64 值。陈旧标记在 `NaN` 被规范化之前直接从原始位识别，并
写入返回样本的 `stale` 字段。

### `decodeColumnarSamples(timestamps: BigInt64Array, values: ArrayBufferLike): Sample[]`

列式布局解码：时间戳与值分属两个 8 字节视图，同样按原始位识别陈旧标记。

## 求值规则（与 Prometheus 对齐）

1. 取落在闭区间 `[evalTs - rangeMs, evalTs]` 内的样本，并过滤掉全部
   `StaleNaN`。陈旧样本只切断相邻关系，**不计为计数器重置**。
2. 有效非陈旧样本少于 2 个 → 返回 `undefined`；其中出现普通 `NaN` →
   按 IEEE 语义返回 `NaN`。
3. 累计相邻增量：计数器在 `v[i] < v[i-1]` 时累加 `v[i]`，否则累加差值；
   gauge 始终累加普通差值。
4. `sampledInterval = t[last] - t[0]`，`avgInterval = sampledInterval / (n-1)`。
5. 两端空隙 `dStart`、`dEnd` 各自与 `1.1 * avgInterval` 比较；达到或超过门限
   时，该端外推时长截断为 `avgInterval / 2`。
6. 计数器零点钳位：当 `isCounter && v[0] >= 0 && resultValue > 0` 且
   `dStart > (v[0] / resultValue) * sampledInterval` 时，把 `dStart` 钳位到
   `(v[0] / resultValue) * sampledInterval`，使回推在零点停止。
7. 外推倍数 `factor = (sampledInterval + dStart + dEnd) / sampledInterval`。
8. `rate = (resultValue / sampledSec) * factor`；
   `increase = rate * (rangeSec + (dStart + dEnd) - avgSec)`。
9. `irate` 只用最后两个非陈旧样本：末值小于前值（计数器）时增量取末值本身，
   再除以两者的秒级间隔。

## 示例

```ts
import { rate, increase } from './src/index.js';

const samples = [
  { t: 0, v: 100 },
  { t: 15000, v: 40 },   // 计数器重置
  { t: 30000, v: 20 },   // 再次重置
];

rate(samples, 30000, 30000);     // 2  (每秒)
increase(samples, 30000, 30000); // 30
```

## 测试

```bash
npm test
```

测试覆盖：多次计数器重置、新进程左边界零点钳位、稀疏采样的
`1.1 * avgInterval` 半间隔截断、`StaleNaN` 与普通 `NaN` 的位级区分（含二进制
缓冲解码）、以及 `irate` 的末尾重置边界。
