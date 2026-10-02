# 时空聚合热力图与密度网格累加器

流式人流/车流时空密度分析器（TypeScript，零依赖）。将地理空间按瓦片网格离散化，
按小时维护时空立方体（Data Cube），支持流式轨迹点 O(1) 落桶累加，
并在任意时空窗口内秒级回答 Top-K 拥堵热点查询。

## 架构

```
src/
  grid.ts    TileGrid                —— 经纬度 <-> 瓦片网格编号映射、网格中心回算
  cube.ts    SpatioTemporalCube      —— 时空立方体：小时切片环形缓冲 + 流式累加 + Top-K 查询
             DensityGridAccumulator  —— 纯空间维度的密度网格累加器（切片内核）
  minHeap.ts BoundedMinHeap          —— 容量受限最小堆，流式 Top-K 选择
```

**内存严格受限**：时间维采用固定槽位的环形缓冲，每个小时槽持有一个
`Float64Array(cellCount)` 密度切片，超出保留时长（`retentionHours`）的最旧小时被自动逐出、
内存原地复用。内存上界 = `retentionHours × cellCount × 8` 字节，与导入点数无关。
例如 200×200 网格保留 7 天（168 小时）仅约 51 MB。

**过期点处理**：立方体维护最新小时水位，早于保留窗口的迟到点直接丢弃并计数；
窗口内的乱序点仍可正确落桶。

## 运行要求

Node.js >= 22.6（利用原生 TypeScript 类型擦除直接运行 `.ts`，无需编译步骤；
Node >= 23.6 默认开启，更早版本加 `--experimental-strip-types`）。

## 使用

```ts
import { SpatioTemporalCube, HOUR_MS } from './src/index.ts';

const cube = new SpatioTemporalCube({
  minLng: 116.0, minLat: 39.6, maxLng: 116.8, maxLat: 40.2,
  cols: 200, rows: 200,        // 4 万个瓦片网格
  retentionHours: 24 * 7,      // 保留最近 7 天的小时切片
});

// 流式累加（百万级轨迹点）
cube.addPoint(116.458, 39.905, Date.now(), 1);

// 查询上周五晚高峰 Top-10 热点网格中心
const top10 = cube.topK(friday + 18 * HOUR_MS, friday + 20 * HOUR_MS, 10);
// => [{ rank, cellIndex, lng, lat, weight }, ...] 按密度降序
```

## 运行样例

```
npm run demo     # 等价于 node examples/demo.ts
```

模拟连续导入 100 万条出租车载客点（55% 聚集在国贸/西二旗/望京三个商圈），
实际输出：

```
=== 流式导入 ===
轨迹点总数:        1,000,000
成功落桶:          1,000,000
丢弃(越界/过期):   0
导入耗时:          321 ms (3.12M 点/秒)

=== 内存占用 ===
立方体内存预算:    51.6 MB (200x200 网格 x 168 小时切片)
进程 RSS:          92.5 MB
堆使用:            8.0 MB

=== 上周五晚高峰(18:00-20:00) Top-10 热点网格 ===
查询耗时:          7.9 ms
排名  中心经度      中心纬度      密度权重
#1    116.4580      39.9045       8579
#2    116.4620      39.9045       7628
#3    116.4580      39.9135       7624
#4    116.4700      39.9915       5722
...（热点网格中心均落在国贸/望京/西二旗商圈，与注入的模拟分布一致）
```

## 测试

Node.js 内置测试运行器（`node:test` + `node:assert`），无任何外部依赖：

```
npm test          # 等价于 node --test 'test/*.test.ts'
```

测试结果（10/10 通过）：

```
✔ TileGrid: 经纬度与网格编号映射及中心回算
✔ TileGrid: 越界点返回 -1
✔ DensityGridAccumulator: 权重累加与重置
✔ BoundedMinHeap: 流式选出 Top-K 且按权重降序
✔ Cube: 流式落桶累加，同一网格跨小时分别计数
✔ Cube: Top-K 热点返回网格中心经纬度且按权重降序
✔ Cube: 查询窗口只统计窗口内小时
✔ Cube: 环形缓冲逐出最旧小时，内存上界恒定
✔ Cube: 越界点与过期点被丢弃并计数
✔ Cube: 百万级点流式导入后秒级完成 Top-10 查询
    [perf] 导入 1000000 点: 100ms, Top-10 查询: 0.2ms
ℹ tests 10  ℹ pass 10  ℹ fail 0
```

覆盖：网格映射与边界、权重累加、Top-K 正确性与排序、时空窗口隔离、
环形缓冲逐出与内存上界、越界/过期点丢弃、百万级性能验收。
