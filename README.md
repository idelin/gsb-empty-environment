# HLC Engine — 混合逻辑时钟（Hybrid Logical Clock）

结合物理墙钟与逻辑计数器的高性能时间戳发生器，提供：

- **严格单调**：物理时钟停滞或短时回拨时，逻辑部分自增，输出绝不后退；
- **因果一致**：`update()` 合并远端时间戳，接收事件的时间戳必然大于发送事件；
- **物理现实约束**：时间戳的物理分量与本地物理时钟的偏差永不超过 `epsilon`
  （默认 500ms），越界时抛出 `ClockDriftError` 而非产生脱离物理现实的时间戳；
- **平滑追赶**：物理时钟向前跃进时，HLC 立即对齐物理时间并将逻辑部分归零。

理论依据：Kulkarni et al., *Logical Physical Clocks* (2014)，
并采用 CockroachDB 风格的硬漂移上界（hard drift bound） enforcement。

## 环境要求

Node.js ≥ 22.6（原生 TypeScript 类型擦除运行；推荐 ≥ 23.6，无需任何 flag）。
无需安装任何依赖。

## 运行

```bash
npm test     # Node.js 内置测试运行器（node:test），16 个用例
npm run demo # 运行样例：停滞 / 回拨 / 跃进 / 因果交换 / 漂移上界
```

## API

```ts
import { HybridLogicalClock, compare, toString, fromString } from './src/hlc.ts';

const hlc = new HybridLogicalClock({ epsilon: 500 }); // now?: () => number 可注入时钟
const ts = hlc.now();          // 本地/发送事件：{ wallTime, logical }
const merged = hlc.update(ts); // 接收事件：合并远端时间戳，保证因果序
```

- `HybridLogicalClock.now()` — 生成严格单调的下一个时间戳；
- `HybridLogicalClock.update(remote)` — 合并远端时间戳，返回因果上更晚的时间戳；
- `HybridLogicalClock.last()` — 最近一次观测到的时间戳（不推进时钟）；
- `compare(a, b)` / `equals(a, b)` — 全序比较；
- `toString(ts)` / `fromString(s)` — 序列化，字典序与因果序一致；
- `ClockDriftError` — 偏差超过 `epsilon` 时抛出，携带 `offset` 与 `epsilon`。

## 验收场景覆盖（test/hlc.test.ts）

| 场景 | 行为 |
| --- | --- |
| 物理时钟正常推进 | `wallTime` 跟随物理时钟，`logical` 恒为 0 |
| 同一毫秒内突发调用 | `logical` 自增，严格单调 |
| 物理时钟停滞 | 逻辑自增保证输出单调不后退 |
| 物理时钟短时回拨 | `wallTime` 保持在历史最大值，逻辑吸收回拨 |
| 物理时钟向前跃进 | 立即对齐物理时间，`logical` 归零 |
| 偏差 > epsilon | 抛出 `ClockDriftError`，拒绝脱离物理现实 |
| 跨节点消息交换 | 接收方时间戳因果上晚于发送方 |
| 远端时间戳越界 | 拒绝合并超出 epsilon 的远端时间戳 |
