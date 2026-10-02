# HLC Engine — Hybrid Logical Clock

高性能混合逻辑时钟（HLC）时间戳发生器（TypeScript，零依赖）。结合物理墙钟与逻辑计数器，提供：

- **严格单调**：物理时钟停滞或短时回拨时，由逻辑计数器保证时间戳绝不后退；
- **因果一致**：`update()` 合并远端时间戳，后续本地时间戳严格大于因果前驱；
- **漂移有界**：HLC 物理分量与真实物理时钟的偏差始终 ≤ `epsilon`（默认 500ms），越界抛出 `ClockDriftError`；
- **平滑追赶**：物理时钟向前跃进时，HLC 立即跟随物理时间并将逻辑计数器归零，不产生脱离物理现实的时间戳。

算法参考 Kulkarni et al. 2014《Logical Physical Clocks and Consistent Snapshots in Globally Distributed Databases》。

## 要求

Node.js ≥ 22.6（利用原生 TypeScript 类型剥离运行 `.ts`，无需构建步骤）。

## 用法

```ts
import { HLCClock } from './src/hlc.ts';

const hlc = new HLCClock({ epsilon: 500 }); // epsilon 可省略，默认 500ms

const ts = hlc.now();          // 本地/发送事件: { wallTime, logical }
const merged = hlc.update(ts); // 接收事件: 合并远端时间戳，保持因果序
```

时间戳可打包为 64 位 `bigint`（48 位毫秒物理分量 + 16 位逻辑计数器），打包后的数值序与字典序一致：

```ts
import { pack, unpack } from './src/hlc.ts';
const packed = pack(ts);   // bigint，可直接比较大小
const back = unpack(packed);
```

## 运行

```sh
npm test    # Node.js 内置测试运行器，15 个用例
npm run demo  # 运行样例：停滞 / 回拨 / 跃进 / 跨节点因果合并
```

## 文件结构

- `src/hlc.ts` — HLC 引擎（`HLCClock`、`Timestamp`、`pack`/`unpack`、漂移与溢出错误类型）
- `test/hlc.test.ts` — 基于 `node:test` 的测试（注入式物理时钟，确定性验证各验收边界）
- `examples/demo.ts` — 可运行样例
