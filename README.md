# 多副本计数器（可重置 CRDT）

操作集语义的多副本计数器：支持递增与显式重置，合并按唯一操作 id 去重，
不依赖本地时钟判断先后。

## 语义

- **递增** `increment(amount)`：产生带唯一 id（`replicaId:seq`）的操作，贡献 `amount`。
- **重置** `reset()`：产生一个显式携带当前已观察递增 id 集合的操作，
  只清除这些贡献；未被观察到的并发递增在合并后保留。
- **合并**：两个操作集分别按 id 取并集，满足交换律、结合律、幂等律；
  消息可任意乱序、重复投递。
- **求值**：`值 = 未被任何 reset 观察到的递增贡献之和`。
  reset 先于其观察的递增到达时，递增到达后仍被清除（纯集合语义，无需时间戳）。

## 使用

```ts
import { CounterReplica } from './src/counter.ts';

const a = new CounterReplica('A');
const b = new CounterReplica('B');

const inc = a.increment(3);   // 作为消息广播
b.receive(inc);               // 任意顺序、任意次数投递均安全
b.reset();                    // 只清除 b 已观察的贡献
a.merge(b.snapshot());        // 或与快照合并
a.value();
```

## 构建与测试

本环境离线（无法安装 tsc），`npm run build` 使用 `scripts/build.mjs`
将 `src/counter.ts`（仅含可擦除类型）擦除为 `dist/counter.mjs`；
若可联网，`npx tsc`（见 `tsconfig.json`）亦可编译同一源码。

```sh
npm test
```

测试覆盖（`test/counter.test.mjs`）：

- 消息排列：8 操作全排列 40320 种 + 随机洗牌，结果均收敛
- 重复合并：操作重复投递、快照重复合并、自合并均幂等
- 重置重叠：并发 reset 观察集相交时清除并集，未观察并发递增保留
- 代数性质：交换律、结合律、幂等律；序列化往返
