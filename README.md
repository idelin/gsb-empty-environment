# 时空轨迹压缩编码与航向角保持器

面向车载终端低带宽传输的轨迹专有压缩器（TypeScript 实现，零依赖）。
依据相邻点的**速度、航向角变化率与高程落差**执行自适应采样：
直线（含直线加速）仅保留起止点，急转弯与变道时密集采样，
在极高压缩比下保持航迹形状，且还原后**航向角与曲线转弯半径零失真**。

## 运行环境

- Node.js ≥ 22.6（利用原生 TypeScript 类型擦除直接运行 `.ts`，无需编译；开发验证于 v23.9.0）
- 无第三方依赖，测试使用 Node 内置 `node:test`

```bash
npm test        # 运行全部测试（node --test）
npm run sample  # 运行端到端样例
```

## 架构

| 模块 | 职责 |
| --- | --- |
| `src/compressor.ts` | 流式自适应采样器 `TrajectoryCompressor`（`push`/`flush`），批量封装 `compress()` |
| `src/encoder.ts` | 紧凑二进制包编解码（`TJC1` 格式，对保留流**无损**） |
| `src/reconstructor.ts` | 接收端轨迹还原（位置线性插值、航向最短弧插值、转弯半径计算） |
| `src/geo.ts` | 大地线距离/方位角/外接圆半径/横偏距等几何原语 |
| `src/metrics.ts` | 形状偏差、地图匹配正确率代理、航向误差等指标 |
| `src/simulator.ts` | 确定性合成驾驶工况（高速巡航/加减速/变道/直角弯/环岛/高架爬坡） |

## 采样策略

每个输入点按以下规则判定（阈值均可配置，见 `CompressorOptions`）：

- `turn-rate`：航向角变化率 |ω| ≥ 6°/s（急转弯、变道）→ 密集采样；
- `curve`：距上一保留点的累计航向变化 ≥ 8°（缓弯）；
- `radius`：瞬时转弯半径 < 250 m；
- `elevation`：距上一保留点高程落差 ≥ 6 m（高架、匝道、隧道）；
- `max-gap`：距上一保留点 ≥ 600 m 或 ≥ 60 s（直线段误差上界）；
- `maneuver-entry`：检测到机动时，额外保留机动起点，使弯道被精确原始端点夹住；
- `boundary`：首末点强制保留。

**零失真的保证**：保留点是原始样本的逐位拷贝；二进制编码对保留流无损
（时间戳 varint 差分、其余字段 f64 原值）。因此还原轨迹在保留点处的
航向角、位置、转弯半径与原始数据完全一致。直线加速段不触发任何
机动/高程规则，仅保留起止点。

## 验收指标（`npm run sample` 实测）

| 指标 | 结果 | 验收线 |
| --- | --- | --- |
| 体积压缩比 | **3.31%**（83 519 B → 2 762 B） | ≤ 12% |
| 点数压缩比 | 11.55%（563 → 65） | — |
| 还原航向角最大误差 | **0°** | 零失真 |
| 最急弯半径误差（22.3722 m） | **0 m** | 零失真 |
| 航迹形状最大偏差 | 1.69 m | — |
| 地图匹配正确率（15 m 容差代理） | **100%** | 保持 |

## 用法

```ts
import { TrajectoryCompressor } from './src/compressor.ts';
import { encodePacket, decodePacket } from './src/encoder.ts';
import { restoreAt } from './src/reconstructor.ts';

// 车载端：流式压缩
const compressor = new TrajectoryCompressor();
const packetChunks = [];
for (const p of rawGnssStream) {
  const kept = compressor.push(p);        // 通常为空数组，机动时返回保留点
  if (kept.length > 0) packetChunks.push(...kept);
}
const tail = compressor.flush();
if (tail) packetChunks.push(tail);
const packet = encodePacket(packetChunks); // 上链传输

// 平台端：解码还原
const kept = decodePacket(packet);
const restored = restoreAt(kept, timestamps); // 任意时刻插值还原
```

## 测试

`test/` 下 17 个用例覆盖：直线加速稀疏化、急转弯/变道密集采样、
高程触发、max-gap 上界、流式与批量一致性、保留点逐位一致、
编解码无损往返、12% 体积红线、航向角/转弯半径零失真、
跨 0°/360° 航向插值等。
