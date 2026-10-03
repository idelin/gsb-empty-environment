# 自适应批大小（Adaptive Batch Size）调节器

面向向量化执行引擎的自适应调度组件。调节器监控当前查询的 **L1/L2 CPU 缓存命中率**
与 **数据行宽**，动态调整每个向量批次（Batch）的行数，使单批工作集尽量容纳在
L1 缓存内，保持缓存热态、避免缓存颠簸（cache thrashing）与内存带宽瓶颈。

## 调节规则（默认参数）

| 条件 | 动作 | 边界 |
| --- | --- | --- |
| 行较宽 / L1 命中率 < 0.60（颠簸、带宽受限） | 批量减半收缩 | 下限 **256** |
| 行极窄 / L1 命中率 ≥ 0.85 且有容量余量 | 批量翻倍放大 | 上限 **4096** |
| 批量超出「恰好装满 L1」的容量上界 | 收缩到容量上界 | 初始批量 **1024** |

容量上界 `capacityBound = clamp(floorPow2(L1容量 / 行宽), 256, 4096)`，
即单批数据刚好容纳在 CPU L1 缓存内的最大 2 的幂批量。

## 目录结构

```
src/regulator.ts     自适应批大小调节器（核心组件）
src/cache-model.ts   L1/L2/内存 缓存层级仿真模型（LRU，64B 缓存行）
src/engine.ts        向量化执行引擎模型（K 级算子流水线 + 反馈回路）
test/*.test.ts       Node.js 内置测试（node:test + node:assert）
test/compat-runner.cjs  旧版 Node（<18，无 node:test）的兼容运行器
demo/run-demo.ts     运行样例
```

## 构建与运行

```bash
npm run build        # tsc 编译到 dist/
npm test             # 编译并运行全部测试（任意 Node 版本）
npm run test:node18  # Node >= 18 时直接用 node --test 运行
npm run demo         # 运行样例
```

> 测试使用 Node.js 内置 `node:test` / `node:assert` API 编写，无任何第三方依赖。
> Node.js >= 18 可直接 `node --test dist/test/`；更老的运行时通过
> `test/compat-runner.cjs` 提供的最小兼容层执行同一批测试文件。

## 运行样例（`npm run demo`）

```
=== 场景 A：超宽宽表查询（行宽 128B） ===
行宽=128B 行数=16384 L1=32KiB L2=256KiB 初始Batch=1024 流水线算子数=8
批次   | 批量  | L1命中率 | L2命中率 | 周期/行 | 调节动作
#    0 |  1024 |   0.0%   |  87.5%   |   396.0 | cache-thrashing-shrink -> 512
#    1 |   512 |   0.0%   |  87.5%   |   396.0 | cache-thrashing-shrink -> 256
#    2 |   256 |  87.5%   |   0.0%   |   214.0 | hold -> 256
#    3 |   256 |  87.5%   |   0.0%   |   214.0 | hold -> 256
#   58 |   256 |  87.5%   |   0.0%   |   214.0 | hold -> 256
结果: 批量 1024 -> 256，L1命中率   0.0% ->  87.5%，每行成本 396.0 -> 214.0 周期

=== 场景 B：极窄行查询（行宽 8B） ===
#    0 |  1024 |  87.5%   |   0.0%   |    13.4 | grow-into-headroom -> 2048
#    1 |  2048 |  87.5%   |   0.0%   |    13.4 | grow-into-headroom -> 4096
#    2 |  4096 |  87.5%   |   0.0%   |    13.4 | hold -> 4096
结果: 批量 1024 -> 4096，L1命中率  87.5% ->  87.5%，每行成本 13.4 -> 13.4 周期

=== 场景 C：中等行宽查询（行宽 32B） ===
#    0 |  1024 |  87.5%   |   0.0%   |    53.5 | hold -> 1024
结果: 批量 1024 -> 1024，L1命中率  87.5% ->  87.5%，每行成本 53.5 -> 53.5 周期
```

场景 A 即验收边界：执行超宽宽表查询（128B/行）时，1024 行/批 = 128KiB 远超
32KiB L1，L1 命中率跌到 0%、miss 全部落到 L2/内存（内存带宽受限）；调节器
动态将每批行数 **1024 → 512 → 256**，256 行 × 128B = 32KiB 恰好装满 L1，
L1 命中率恢复到 87.5%（8 级流水线中仅第 1 级冷 miss），每行成本下降约 46%。

## 测试结果（`npm test`）

```
ok 1 - 验收边界：超宽宽表查询（行宽 128B） > 调节器感知内存带宽受限，将批量从 1024 动态下调至 256
ok 2 - 验收边界：超宽宽表查询（行宽 128B） > 256 行单批恰好容纳在 32KiB L1 缓存内
ok 3 - 验收边界：超宽宽表查询（行宽 128B） > 调整后 L1 命中率恢复热态（>= 0.85），避免缓存颠簸
ok 4 - 验收边界：超宽宽表查询（行宽 128B） > 调整后每行处理成本显著下降
ok 5 - 行极窄的查询（行宽 8B，单列投影） > 缓存保持热态，批量放大到 4096
ok 6 - 中等行宽的查询（行宽 32B） > 初始 1024 恰好等于 L1 容量上界，批量保持稳定
ok 7 - floorPow2 > 向下取 2 的幂
ok 8 - AdaptiveBatchSizeRegulator.capacityBoundFor > 行宽 128B 时容量上界为 256（256 行恰好装满 32KiB L1）
ok 9 - AdaptiveBatchSizeRegulator.capacityBoundFor > 行宽 32B 时容量上界为 1024
ok 10 - AdaptiveBatchSizeRegulator.capacityBoundFor > 行极窄（8B / 4B）时容量上界顶到 maxBatchSize 4096
ok 11 - AdaptiveBatchSizeRegulator.capacityBoundFor > 行超宽（大于 L1）时容量上界夹到 minBatchSize 256
ok 12 - AdaptiveBatchSizeRegulator 收缩行为（宽行 / 缓存颠簸） > L1 命中率过低时从 1024 逐步减半到 256 并停住
ok 13 - AdaptiveBatchSizeRegulator 收缩行为（宽行 / 缓存颠簸） > 批量永远不会低于 minBatchSize 256
ok 14 - AdaptiveBatchSizeRegulator 收缩行为（宽行 / 缓存颠簸） > 命中率尚可但批量超出 L1 容量时直接收缩到容量上界
ok 15 - AdaptiveBatchSizeRegulator 放大行为（窄行 / 缓存热态） > L1 命中率高时从 1024 翻倍放大到 4096 并封顶
ok 16 - AdaptiveBatchSizeRegulator 放大行为（窄行 / 缓存热态） > 放大不超过 L1 容量上界
ok 17 - AdaptiveBatchSizeRegulator 稳定保持 > 命中率处于水位之间且批量不超容量时保持不动
ok 18 - AdaptiveBatchSizeRegulator 稳定保持 > 历史记录包含 from/to/reason/hitRate 等观测信息

# tests 18
# pass 18
# fail 0
```

## API 速览

```ts
import { AdaptiveBatchSizeRegulator } from "./src";

const regulator = new AdaptiveBatchSizeRegulator({
  minBatchSize: 256,      // 行较宽时收缩到此值
  maxBatchSize: 4096,     // 行极窄时放大到此值
  initialBatchSize: 1024,
  l1CacheBytes: 32 * 1024,
  l2CacheBytes: 256 * 1024,
});

// 每个批窗口反馈一次缓存统计与当前行宽，返回调整后的批量
const next = regulator.observe(
  { l1Hits: 0, l1Misses: 16384, l2Hits: 14336, l2Misses: 2048 },
  128, // 行宽（字节）
);
// next === 512（下一步继续减半到 256 后稳定）
```
