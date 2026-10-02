# rational-timecode

有理数帧率下「时间码标签 ↔ 连续帧号 ↔ 音频样本号」的精确互转库。全程 BigInt
有理数运算，不经过 `Number` 中转大数，不使用浮点累计。TypeScript 编写，
Node.js ≥ 23 直接运行（内置类型擦除），测试使用 Node 内置 `node:test`。

## 运行

```sh
npm test    # node --test（自动发现 test/*.test.ts）
npm run demo
```

## 核心概念

**Drop-frame 是标签规则，不是丢帧。** 帧的连续编号永远逐帧 +1；drop 表只是
声明某些标签不存在。规则由显式周期表描述（`compileRule`）：

```ts
const df2997 = compileRule(
  "ntsc-29.97-df",
  { nominalFps: 30,        // 标称整数帧率（标签速率）
    periodSeconds: 60,     // drop 表周期：每分钟
    dropFrameLabels: [0,1],// 每个 drop 周期开头不存在的帧标签
    keepEveryPeriods: 10 },// 每第 10 个周期豁免（NTSC：整 10 分钟不跳号）
  rat(30000n, 1001n),      // 实际帧率（有理数）
);
```

编译产物是「有效标签 → 连续帧号」的**双向排名索引**：正反向转换都按周期
（及豁免超周期）做 BigInt 整除跳跃，O(1) 完成，绝不逐帧扫描——10^15 帧
（约 1056 年）照常往返。

## 固定策略

- **舍入显式**：所有帧↔样本转换必须指定 `floor | ceil | trunc | half-up |
  half-even`（half-up = 半值远离零；half-even = 半值取偶邻）。库内无默认
  “就近”行为。精确值始终以 `Rational` 提供（`*Exact` 系列）。
- **非法标签**：被 drop 表排除的标签不存在。严格转换抛
  `InvalidLabelError`（`reason: "dropped"`，附 `nearest` 候选）；
  `parseLabelOrNearest` 返回最近合法候选（距离并列时取向前）。
- **标签形态**：`HH:MM:SS<sep>FF`。HH ≥ 2 位且上不封顶；MM/SS 恰好 2 位
  且 < 60；FF 宽度 = `nominalFps-1` 的十进制位数（至少 2 位）。drop 规则
  要求 `;`，非 drop 规则要求 `:`，分隔符不匹配即拒绝。
- **负时间**：不支持。帧号与标签从 0 = `00:00:00:00` 起；负输入抛错。

## 跨规则版本拼接与音频对齐

`Timeline` 把不同规则版本（不同帧率/drop 表）的片段拼成一条输出时间线：

- `cutTime(boundary)`：切点精确时刻 = 各段 `frames/fps` 的有理数和。
- `audioCut(boundary, sampleRate, mode)`：切点对齐到采样网格，返回
  `{ time, sample, error }`，其中 `error = sample/sampleRate − time`
  为**有理数**（秒，带符号）。

## 文件

- `src/rational.ts` — BigInt 有理数与显式舍入
- `src/timecode.ts` — drop 表编译、双向排名索引、标签解析、帧↔样本
- `src/timeline.ts` — 跨规则拼接与音频样本对齐
- `test/` — `node:test` 测试（周期边界穷举往返、长时段周期跳跃等）
- `examples/demo.ts` — 运行样例
