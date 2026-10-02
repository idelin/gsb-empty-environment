# traj-compact · 时空轨迹压缩编码与航向角保持器

面向车载终端低带宽传输的轨迹专有压缩器（TypeScript，零依赖，Node.js ≥ 22.6 直接运行）。

## 特性

- **流式自适应采样**：依据相邻点的速度、偏航角速度（航向角变化率）与高程落差逐点决策
  - 直线加速 / 匀速巡航：仅保留起止点
  - 急转弯（偏航角速度 ≥ 12°/s）：自动切换 0.5° 航向步长，密集采样
  - 变道（弧长窗口内航向先偏后回）：保留摆幅极值点
  - 高程落差 ≥ 3m 自动补点
- **航迹形状保证**：在线 Douglas-Peucker 式弦偏差递归检查，被丢弃点到还原折线的垂直偏差有界（默认 4m）
- **航向角 / 转弯半径零失真**：航向、速度、高程、时间戳经整数网格状态递推编码，网格值逐位无损；非网格值自动回退 float64 字面量，依然无损
- **高压缩比**：zigzag 变长增量编码，混合工况下数据包体积 ≤ 原始 JSON 负载的 12%（样例实测 1.78%）

## 数据包格式（TRJ1）

```
magic "TRJ1" (4B) | uvarint 点数 | 首点 6×f64 | 后续点: flags(1B) + 增量字段
flags: bit0 航向字面量 | bit1 高程字面量 | bit2 速度字面量 | bit3 时间戳字面量
经纬度: 1e-7° 网格 zigzag delta（≈1.1cm，低于车载 GPS 噪声）
航向: 0.01° 网格 zigzag delta，非网格值回退 f64
速度: 1mm/s 网格 zigzag delta，非网格值回退 f64
高程: 1cm 网格 zigzag delta，非网格值回退 f64
时间戳: 整数毫秒 zigzag delta，非整数回退 f64
```

## 用法

```ts
import { TrajectoryCompressor, compressTrajectory, decodeTrajectory } from './src/index.ts';

// 流式（车载终端逐点上报）
const c = new TrajectoryCompressor({ maxChordDeviationM: 4.0 });
for (const p of gpsStream) c.push(p);   // p: { lat, lon, alt, speed, heading, t }
const packet: Uint8Array = c.flush();   // 上传数据包

// 一次性
const packet2 = compressTrajectory(points);

// 还原
const points = decodeTrajectory(packet);
```

## 运行

```bash
npm run demo   # 运行样例：混合工况轨迹 → 压缩 → 还原 → 保真度校验
npm test       # Node.js 内置测试运行器，12 项验收测试
```

## 运行样例输出

```
=== 时空轨迹压缩编码 · 运行样例 ===
原始点数:        650
保留点数:        63  (采样率 9.7%)
原始体积(JSON):  87232 B
原始体积(二进制): 31200 B
压缩后体积:      1556 B
压缩率(vs JSON):  1.78%
压缩率(vs 二进制): 4.99%
验收线 ≤12%:     通过

--- 分工况采样 ---
straight-accel   原始 300 点 → 保留   1 点
sharp-turn       原始  30 点 → 保留  30 点
cruise           原始 100 点 → 保留   0 点
lane-change      原始  40 点 → 保留   4 点
s-curve          原始  80 点 → 保留  21 点
hill-climb       原始 100 点 → 保留   7 点

--- 还原保真度 ---
航向角零失真:    通过（逐位一致）
转弯半径零失真:  通过（逐位一致）
```

## 测试结果

```
✔ 直线加速仅保留起止点
✔ 急转弯密集采样
✔ 变道触发额外采样点
✔ 数据包体积压缩至原始数据的 12% 以内
✔ 还原后航向角零失真（网格值与非网格值）
✔ 还原后曲线转弯半径零失真
✔ 速度/高程/时间戳零失真，经纬度误差 ≤1e-7°
✔ 地图匹配正确率保持：还原点吸附位置与原始一致
✔ 极高压缩比下航迹形状保持（弦偏差有界）
✔ 流式逐点压缩与一次性压缩结果一致
✔ 空轨迹与单点轨迹
✔ 解码器拒绝非法数据包
ℹ tests 12  ℹ pass 12  ℹ fail 0
```

## 目录结构

```
src/types.ts         轨迹点与采样阈值定义
src/geo.ts           航向差/haversine/弦偏差/转弯半径
src/codec.ts         zigzag varint 与 f64 二进制读写
src/compressor.ts    流式自适应采样压缩器（核心）
src/decompressor.ts  解码器（整数网格状态递推，逐位无损）
examples/scenario.ts 混合工况轨迹生成器（确定性种子）
examples/demo.ts     运行样例
test/compressor.test.ts  12 项验收测试
```
