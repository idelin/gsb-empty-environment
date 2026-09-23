# 可更正的重采样器

把不规则采样转换到固定时间网格，缺测段不会被一条直线掩盖：
显式断点与超过上限的缺测间隔都会切断插值，迟到采样与撤回以修订形式更正输出。

## 规则定义

- **网格**：`t_k = t0 + k*step`，用 `Fraction` 精确表示，输出时间严格落在网格位置上，无浮点漂移。
- **模式**：
  - `step`（右连续阶梯）：`t` 处取最后一个采样时刻 `<= t` 的样本。
  - `linear`（线性）：`t` 处取两侧最近样本线性插值；`t` 恰有样本时为直接观测。
- **禁止跨越**：
  - 断点 `b`：线性模式下落在插值区间 `(s1, s2)` 内、或阶梯模式下满足 `s < b <= t`，输出无效。
  - 缺测上限 `max_gap`：线性按相邻样本间隔 `s2 - s1`、阶梯按 `t - s` 判定，超限输出无效。
- **同一时刻多样本**：ID 大（晚到达）者生效；撤回生效样本后，被遮蔽的旧样本复活。
- **精度与舍入**：插值在 `Fraction` 上精确计算，输出值统一量化到 `1e-6`、`ROUND_HALF_EVEN`（`VALUE_QUANT` / `VALUE_ROUNDING` 常量固定）。

## 接口

```python
rs = Resampler(t0, step, count, mode="linear", max_gap=None)
sid, revs = rs.ingest(t, value)   # 摄入（允许迟到）-> (样本ID, 修订)
revs = rs.retract(sid)            # 撤回 -> 修订
revs = rs.add_breakpoint(t)       # 断点增删 -> 修订
revs = rs.remove_breakpoint(t)
rs.query(k) / rs.grid()           # 查询单个 / 全部 GridPoint
rs.compute_range(k0, k1)          # 从当前状态重算一段（分块对照用）
final = rs.seal(watermark)        # 封闭区间，返回最终网格点
```

每个 `GridPoint` 携带：`index`、`time`（精确 `Fraction`）、`mode`、`valid`、
`value`（量化 `Decimal`）、`source_ids`（所用原始采样 ID）、`reason`（无效原因：
`no-sample` / `gap-exceeded` / `breakpoint`）。
`Revision` 携带 `index`、`time` 与 `old`/`new` 两个 `GridPoint`。

## 修订消费示例

`GridConsumer`（见 `resampler.py` 与 `example.py`）演示下游如何只凭修订流恢复完整网格：
先 `bootstrap` 初始快照，之后逐条 `apply(revisions)`，任意时刻都与重采样器输出一致。

## 整段参考 vs 随机分块

`compute_reference(...)` 是与增量引擎共用同一核心判定的整段批量实现。
`test_resampler.py` 中的随机测试对任意操作序列（摄入/撤回/断点增删）按随机大小分块执行，
每个分块后三方对照：增量状态 == 批量参考 == 修订消费者；同时把网格按下标随机分块
调用 `compute_range` 重算，拼接结果也必须等于整段参考。

## 历史样本保留与封闭区间释放

**必须保留**：
- 水位线之后的全部样本，包括同一时刻被更高 ID 遮蔽的样本（撤回会令旧样本复活）；
- 全部断点；全部已输出网格点（用于 diff 产生修订）。

**`seal(watermark)` 后可释放**（调用方承诺不再出现 `t < watermark` 的摄入/撤回/断点）：
- `t < watermark` 的样本，**除边界样本**（`watermark` 前最后一个有时刻的样本，
  它是线性左邻与阶梯持有的最后来源，其同刻遮蔽样本一并保留）；
- `t < watermark` 的断点，**除最后一个**（它与边界样本共同决定跨越判定：
  若它晚于边界样本，则水位线后由边界样本支撑的输出仍应被判为跨断点无效）；
- `t < watermark` 的输出网格点即为最终值，可归档后截断（本实现保留在内存中，
  生产系统可 checkpoint 后释放）。

**释放后的行为**：早于水位线的摄入/断点抛 `ValueError`；已释放样本 ID 的撤回抛
`KeyError`；边界样本仍可撤回（会正常产生修订）。水位线只能前进不能后退。

## 运行

```bash
python3 -m unittest -v   # 测试
python3 example.py       # 端到端示例
```
