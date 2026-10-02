# 时空聚合热力图与密度网格累加器

流式人流/车流时空密度分析器（TypeScript，零依赖）。将地理空间按瓦片网格离散化，
按小时维护时空立方体（Data Cube），支持流式轨迹点 O(1) 落桶累加，并可对任意
时空窗口秒级回答 Top-K 拥堵热点（网格中心经纬度）。

## 运行环境

- Node.js >= 23.6（原生类型擦除，直接运行 `.ts`，无需编译/依赖）

```bash
npm test    # node --test test/*.test.ts（Node 内置测试运行器）
npm run demo  # 百万级轨迹点导入 + 上周五晚高峰 Top-10 查询样例
```

## 架构

| 模块 | 职责 |
| --- | --- |
| `src/grid.ts` | `TileGrid`：bbox + 瓦片边长（米）→ 行×列网格；`cellOf` 经纬度落桶，`centerOf` 返回裁剪到 bbox 的瓦片中心 |
| `src/cube.ts` | `SpatioTemporalCube`：小时桶 × 瓦片的立方体；固定容量 `Float64Array` 层环形复用，超容量逐出最旧小时层，内存严格上界 = `capacityHours × cells × 8` 字节 |
| `src/topk.ts` | 固定大小最小堆，O(n log k) 选出权重最大的 K 个网格 |
| `src/accumulator.ts` | `DensityAnalyzer`：组合以上三者；`addPoint` 流式累加，`queryTopK(startMs, endMs, k)` 窗口聚合 + Top-K |

关键性质：

- **写入 O(1)**：经纬度 → 网格下标 → 小时桶 → 数组元素累加。
- **内存严格受限**：立方体只保留 `capacityHours` 个小时层（默认 744 ≈ 一个月），
  逐出时复用缓冲区，无 GC 压力；与导入点数无关。
- **查询快**：窗口内小时层向量相加 + 大小为 K 的最小堆，实测百万点导入后
  单月网格（744h × 18358 格）Top-10 查询约 40 ms。

## 使用示例

```ts
import { DensityAnalyzer } from './src/index.ts';

const analyzer = new DensityAnalyzer({
  grid: { minLat: 39.75, minLng: 116.20, maxLat: 40.05, maxLng: 116.60, tileSizeMeters: 250 },
  capacityHours: 24 * 31,
});

// 流式导入出租车载客点
analyzer.addPoint(39.908, 116.457, Date.now(), 1);

// 上周五晚高峰 18:00-20:00 全市密度最高的前 10 个网格中心
const top10 = analyzer.queryTopK(friday18, friday20, 10);
// => [{ rank, cell, lat, lng, weight }, ...]
```

完整可运行样例见 `examples/demo.ts`（模拟 100 万载客点，含 CBD/三里屯等热点簇）。

## 实测结果（Apple Silicon，Node v23.9.0）

`npm run demo`：

```
Ingest:  1523 ms (656446 pts/s)     # 1,000,000 点流式累加
Query:   40.5 ms                    # 2 小时窗口 Top-10
Memory:  cube bound 104.2 MiB (744 h x 18358 cells), process RSS 110.4 MiB
Stats:   1,000,000 added, 0 dropped, 0 evictions
Top-10 density hotspots, 2026-10-02 18:00 - 2026-10-02 20:00:
  # 1  lat 39.93752, lng 116.44736  weight 33273   （三里屯）
  # 2  lat 39.90833, lng 116.45615  weight 26567   （国贸/CBD）
  ...
```

`npm test`：19 个用例全部通过，覆盖网格落桶/中心回环、小时层累加、
容量逐出与内存上界、Top-K 堆正确性、窗口过滤、越界丢弃，以及
百万点导入 < 10 s、窗口查询 < 1 s 的性能断言。
