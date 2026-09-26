# normplan — 离线音量归一化计划器

Python 3.11+，仅标准库。对给定样本数组与采样率（由本地夹具提供，不接音频设备）
联合求解满足以下约束的逐片段增益曲线：

- 每片段目标 RMS（dBFS）
- 全局输出峰值上限 `peak_ceiling`
- 相邻片段最大增益变化比 `max_adjacent_ratio`

静音片段不放大（增益恒 ≤ 1.0）；短片段与重叠淡化区按真实采样能量计算；
约束不可同时满足时返回显式冲突与残差，`normalized=False`，不伪称已归一。

## 声明的延迟与数值容差

- `PLANNER_LATENCY`：离线规划，需要完整输入信号（整文件前瞻）。
- `APPLY_LATENCY_SAMPLES = 0`：增益是样本索引的纯函数，应用无额外前瞻。
- `RMS_TOLERANCE_DB = 0.1 dB`：片段 RMS 残差容差。
- `PEAK_TOLERANCE = 1e-6`：输出峰值上限的相对容差。
- 分块应用与整块应用逐比特一致（`gain_at(n)` 与分块边界无关）。

## 结构

- `normplan/planner.py` — 约束求解、交叉淡化峰修复、冲突/残差报告、计划序列化
- `normplan/apply.py` — 输入摘要验证、分块应用、可取消的落盘写出（`.part` + `.complete` 标记）
- `normplan/fixtures.py` — 确定性本地夹具（样本数组 + 采样率）
- `tests/test_normplan.py` — unittest 测试
- `examples/demo.py` — 可运行样例

## 用法

```python
from normplan import fixtures, Segment, plan_gains, apply_plan

samples, sr = fixtures.loud_quiet_tone()
result = plan_gains(samples, sr,
                    [Segment(0, 4000, -30.0), Segment(4000, 8000, -12.0)],
                    peak_ceiling=0.98, max_adjacent_ratio=4.0)
if result.normalized:
    out = apply_plan(result.plan, samples)          # 或任意分块
else:
    for c in result.conflicts:
        print(c.detail)                              # 冲突原因与残差
```

计划可 `plan.to_json()` / `Plan.from_json()` 序列化；`apply_plan` 默认校验
输入摘要（SHA-256，覆盖 float32 样本流与采样率），源音频被更正后旧计划
抛出 `PlanInvalidError`。`apply_plan_to_file` 只有在完整渲染后才发布
`.complete` 标记，取消时不发布。

## 运行

```
python3 -m unittest discover -s tests -t . -v
python3 examples/demo.py
```

## 测试结果

```
Ran 13 tests in 0.063s

OK
```

覆盖：相邻目标差过大 + 严格平滑限制 → 报告 `infeasible_pair`；等功率交叉
淡化区峰值超过任一单片段 → 最终全信号检查捕获并修复；固定计划整块与随机
分块应用逐比特一致；源音频更正使计划失效；取消写出不发布完整文件标记；
静音不放大；短片段与重叠区真实能量；序列化往返一致。
