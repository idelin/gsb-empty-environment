# sliding-window-histogram

滑动窗口直方图与高百分位数（P90/P99）估算器，TypeScript 实现，零运行时依赖。

## 设计

**混合分桶**（`src/histogram.ts`）：

- 固定桶宽区：`[0, 128ms)` 分为 512 个 0.25ms 桶，代表值为桶中点，
  最大绝对误差 0.125ms。
- 对数桶区：`[128ms, 2^27 ms)` 每倍频程 128 桶（比率 `r = 2^(1/128)`），
  代表值为桶界几何均值，最大相对误差 `2^(1/256) - 1 ≈ 0.2713%`，
  即理论误差界限，满足 0.5% 验收要求。

**滑动窗口与反向补偿**：

- 窗口容量 1000 个样本；环形缓冲只为每个在窗样本保留一个 4 字节桶索引，
  不常驻原始明细样本，内存 `O(capacity + buckets)`（约 1000 桶索引 + ~3100 桶计数）。
- 样本滑出时对其桶计数做反向补偿（递减），新样本递增对应桶，计数和恒等于窗口大小。
- 分位数采用最近秩（nearest-rank）法：沿桶计数累计至 `ceil(q * N)` 所在桶，
  返回该桶代表值。因桶序与值序一致，估计值与精确分位数同处一桶，
  误差被桶宽理论界限约束。

## 使用

```ts
import { SlidingWindowHistogram } from './src/histogram';

const hist = new SlidingWindowHistogram({ capacity: 1000 });
hist.record(23.5);   // 延迟样本，单位 ms
hist.p90();          // 流式 P90
hist.p99();          // 流式 P99
```

## 构建 / 测试 / 示例

需要任意 `tsc >= 3.9`（本仓库为离线环境，未安装 node_modules；
已提交编译产物 `dist/`，可直接用 node 运行）：

```sh
tsc -p tsconfig.json            # 构建到 dist/
node dist/test/histogram.test.js  # 内置 assert 测试（Node 内置模块，无外部框架）
node dist/examples/demo.js        # 运行样例
```

> 说明：当前环境 Node 为 16.14，尚无 `node:test` 运行器（16.17 引入），
> 测试使用 Node 内置 `assert` 模块 + 轻量 TAP 风格输出，无任何外部依赖。

## 测试结果

```
ok 1 - empty window yields NaN quantiles
ok 2 - window fills, then evicts with reverse compensation
  # long-tail stream: 190 checkpoints, max rel err p90=0.1247% p99=0.2705%
ok 3 - P90/P99 stay within 0.5% of exact on a long-tail stream
ok 4 - P99 falls back quickly after extreme spikes slide out
  # spike test: baseline=174.76ms during=60586.92ms after=185.48ms exact=185.43ms relErr=0.0316%
ok 5 - error stays within the theoretical bucket bound at every checkpoint
ok 6 - fixed-region samples: absolute error bounded by half bucket width
ok 7 - small windows and single sample behave correctly
1..7
# pass 7, fail 0
```

验收要点：

- 窗口容量 1000，连续压入 20000 个长尾（对数正态）样本，190 个检查点上
  P90/P99 与全量保留样本的精确值误差最大 0.2705% < 0.5%，且在理论界限
  0.2713% 之内。
- 极端毛刺（50 个 ~60s 样本）使 P99 从 ~175ms 升至 ~60.6s；毛刺全部滑出
  窗口后 P99 回落至 ~185ms，与精确 P99 误差 0.0316%。

## 运行样例输出（`node dist/examples/demo.js`）

```
step | window | hist P90 | exact P90 | err% | hist P99 | exact P99 | err%
-----+--------+----------+-----------+------+----------+-----------+-----
2000 |   1000 |    88.13 |     88.16 | 0.039 |   187.50 |    187.36 | 0.078
2049 |   1000 |   112.38 |    112.42 | 0.037 | 60915.90 |  60865.18 | 0.083   <- 毛刺进入
3000 |   1000 |   138.46 |    138.15 | 0.218 | 60915.90 |  60865.18 | 0.083
3050 |   1000 |   100.38 |    100.25 | 0.124 |   235.39 |    235.76 | 0.156   <- 毛刺滑出，P99 回落
5999 |   1000 |    92.13 |     92.01 | 0.123 |   240.54 |    240.05 | 0.205
```
