# loudnorm — 离线音量归一化计划器

纯 Python 标准库实现（3.11+），不接音频设备。样本数组与采样率由本地夹具
（`loudnorm/fixtures.py`）提供。规划器联合三类约束求可行增益曲线：

1. **片段目标 RMS**：期望增益 = 目标 / 实测 RMS（真实能量，短片段按实际
   样本计算，不做 amp/√2 之类的近似）；
2. **全局峰值上限**：每段增益上限 = ceiling / 段内峰值；
3. **相邻片段最大增益变化率**：`|g[i+1] − g[i]| ≤ max_gain_step`（线性增益
   差），通过前向/后向投影得到满足速率限制的逐点最大曲线。

## 关键行为

- **静音不放大**：RMS 低于 `SILENCE_RMS_THRESHOLD`（1e-4，−80 dBFS）的段
  增益恒为 1.0。
- **重叠淡化区**：边界处线性交叉淡化（`fade_len` 采样，居中并裁剪到相邻段
  内），淡化区能量/峰值按真实样本计算。
- **交叉区峰值**：最终峰值检查作用于*渲染后*的整条曲线（含交叉淡化区），
  因为高增益段与响段的交叉区可能超过任一单段的缩放峰值；超限时迭代压低
  相关段增益修复，修复事实记入 `plan.notes`。
- **诚实报告**：目标不可同时满足时，`plan.status == "partial"`，
  `plan.conflicts` 逐段给出 dB 残差与归因（`peak_ceiling` /
  `gain_rate_limit_with_segment_k` / `crossfade_peak_repair`），绝不伪称
  已归一（`plan.is_normalized` 仅在无冲突时为真）。
- **可序列化**：`Plan.to_json()/from_json()` 往返无损；计划携带输入摘要
  （SHA-256，采样率 + 长度 + float32 量化样本）。
- **分块应用**：`apply_plan(..., chunk_size=k)` 对任意 k 与整块应用逐位
  一致（增益仅是采样序号的纯函数）。
- **输入摘要验证**：源音频被更正后摘要失配，`apply_plan`/`write_output`
  抛出 `PlanMismatchError`，旧计划失效。
- **原子写出**：`write_output` 先写 `.part` 再原子改名，成功后才发布
  `.complete` 标记（含输入摘要）；取消（`cancel_check`）时清理部分文件，
  不发布完整文件标记。

## 延迟与数值容差（声明于 `loudnorm/constants.py`，并嵌入每个计划）

- 规划为离线，需要完整信号；应用计划的稳态处理延迟为 `fade_len // 2`
  采样（`plan.latency_samples`）。
- `GAIN_TOLERANCE_DB = 0.1`：欠目标超过该值即报冲突。
- `PEAK_TOLERANCE = 1e-6`：渲染峰值检查的线性容差。
- `SILENCE_RMS_THRESHOLD = 1e-4`：静音判定阈值。

## 用法

```python
from loudnorm import SegmentSpec, apply_plan, plan_normalization, write_output
from loudnorm import fixtures

samples, rate = fixtures.join(fixtures.tone(220, 0.5, 0.1)), fixtures.SAMPLE_RATE
specs = [SegmentSpec(0, len(samples), 0.2)]
plan = plan_normalization(samples, rate, specs,
                          peak_ceiling=0.9, max_gain_step=5.0, fade_len=256)
assert plan.is_normalized
out = apply_plan(samples, rate, plan, chunk_size=1024)   # 任意块大小结果一致
write_output("/tmp/out.f32", samples, rate, plan)        # 生成 .complete 标记
```

## 运行

```bash
python3 examples/run_example.py          # 可运行样例
python3 -m unittest discover -s tests -t . -v   # 测试（24 项）
```

## 布局

- `loudnorm/constants.py` — 容差与延迟声明
- `loudnorm/audio.py` — 真实 RMS/峰值、输入摘要
- `loudnorm/curve.py` — 增益曲线与交叉淡化
- `loudnorm/planner.py` — 联合约束求解、冲突与残差
- `loudnorm/plan.py` — 可序列化计划
- `loudnorm/apply.py` — 摘要验证与分块应用
- `loudnorm/writer.py` — 原子写出与取消
- `loudnorm/fixtures.py` — 本地样本夹具
- `examples/run_example.py`、`tests/` — 样例与 unittest
