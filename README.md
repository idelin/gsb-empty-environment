# 滑动窗口连续条件持续时间积分器

计算流式指标在滑动窗口内满足布尔条件的**累计毫秒数**。不采用离散数值积分
（黎曼求和），而是记录条件状态的**突变点时间戳**，查询时对满足区间与当前
窗口做解析求交——窗口滑动把突变点移出时按精确时间戳扣减贡献，从根本上
消除累积漂移。

## 运行要求

- Node.js ≥ 22.6（使用内置 `--experimental-strip-types` 直接运行 TypeScript，
  以及内置 `node:test` 测试运行器，无需安装任何依赖）

```bash
npm test    # 运行全部测试
npm run demo # 运行验收场景示例
```

可选：`npm install` 后可用 `npm run typecheck`（tsc --noEmit）做静态类型检查。

## 使用方式

```ts
import { SlidingWindowConditionIntegrator } from "./src/sliding-window-integrator.ts";

const integrator = new SlidingWindowConditionIntegrator(
  100_000,                        // 窗口宽度：100 秒
  (temperature) => temperature > 80, // 布尔条件：温度 > 80°C 视为告警
);

integrator.addSample(10_000, 85); // t=10s 升至 85°C
integrator.addSample(40_000, 75); // t=40s 降至 75°C

integrator.accumulatedMs(50_000);  // => 30000（30 秒）
integrator.accumulatedMs(120_000); // => 20000（t=10s 滑出，平滑扣减中）
integrator.accumulatedMs(140_000); // => 0（告警段完全滑出，归零）
```

## 验收场景对照

窗口 100s；t=10s 温度 75→85（进入告警），t=40s 温度 85→75（退出告警），
告警区间为 [10s, 40s)：

| 查询时刻 | 窗口范围      | 窗口内告警交集 | 累计超温时间 |
| -------- | ------------- | -------------- | ------------ |
| t=50s    | [-50s, 50s]   | [10s, 40s]     | **30s**      |
| t=110s   | [10s, 110s]   | [10s, 40s]     | 30s          |
| t=120s   | [20s, 120s]   | [20s, 40s]     | 20s          |
| t=130s   | [30s, 130s]   | [30s, 40s]     | 10s          |
| t=140s   | [40s, 140s]   | 空             | **0s**       |

说明：t=110s 起窗口左边界越过 t=10s 突变点，此后每经过 1s 精确扣减 1s，
随窗口滑动**平滑线性衰减**，至 t=140s 完全归零——即“突变点滑出窗口后
超温时间平滑归零”的精确过程（t=120s 时处于衰减中点，剩余 20s）。

## 设计要点

- **突变点驱动**：`addSample` 仅在条件求值结果翻转时记录
  `{timeMs, active}` 突变点，稳态数据零开销。
- **解析求交**：`accumulatedMs(nowMs)` 将满足区间与
  `[nowMs - windowMs, nowMs]` 求交并求和，结果只取决于突变点的精确
  时间戳，与采样频率、查询次数无关，重复查询幂等。
- **精确扣减与内存有界**：滑出窗口的突变点被压缩为“窗口左边界处的
  状态”（`baseActive`），其历史贡献被精确移除，保留的突变点数量有界。
- **亚毫秒精度**：时间戳支持非整数毫秒，扣减按浮点时间戳精确进行，
  无定点/步长量化误差。

## 项目结构

- `src/sliding-window-integrator.ts` — 积分器实现
- `test/sliding-window-integrator.test.ts` — Node.js 内置测试（10 个用例）
- `examples/demo.ts` — 验收场景运行样例
