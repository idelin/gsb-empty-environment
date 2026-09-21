# 可更重的重采样器（correctable resampler）

把不规则采样转换到固定时间网格，缺测段保持“未知”，绝不用一条直线掩盖。
纯标准库实现（`Fraction` / `Decimal`），无第三方依赖。

## 接口

- 摄入：`add_sample(t, value, sample_id=None)`、`add_breakpoint(t)`
- 撤回：`retract(sample_id)`、`remove_breakpoint(t)`
- 查询：`point_at(k)`、`grid_range(k0, k1)`（含端点，k 可为负）
- 修订：`revisions_since(version)` → `[RevisionEvent]`；`version` 属性
- 生命周期：`close_until(t)` 封闭区间并释放历史

每次变更调用是一个事务：`version` 增 1，返回本次产生的修订事件。
网格点 `k` 的时间为 `t0 + k*dt`，用 `Fraction` 精确表示（`dt="0.1"` 时
`k=3` 精确为 `3/10`，不是 `0.30000000000000004`）。

## 两种模式

- `step`（右连续阶梯）：`value(t)` = 时间 ≤ t 的最近样本值。
- `linear`（线性）：由左右相邻样本线性插值；样本范围外不外推；
  网格点恰好落在样本上时取该样本原值。

## 禁止跨越（输出转为 unknown）

- 显式断点：`step` 下断点出现在 `(样本时刻, t]` 即失效；`linear` 下断点
  严格落在插值区间内部即失效（断点与端点样本重合不算跨越）。
- 缺测间隔：相邻样本间隔 > `max_gap`（linear），或最近样本距今 >
  `max_gap`（step），区间内网格点全部为 unknown，原因为
  `gap-exceeds-max`。

## 精度与舍入（固定规则）

所有输出值量化为 6 位小数、`ROUND_HALF_UP`；插值在 `Fraction` 上精确
计算后再量化，不产生浮点误差累积。每个 `GridPoint` 携带：
`index / t / value / valid / mode / sample_ids / reason`。

## 同时间多样本

同一时刻的多个样本按到达顺序裁决：后到的生效；撤回生效者后，前一个
自动重新浮出水面（并产生修订）。

## 修订模型

迟到样本、撤回、断点增删都会只重算“受影响区间”（由相邻样本、断点与
`max_gap` 界定的保守闭区间），与已发出状态做 diff 产生
`RevisionEvent(old, new)`。消费者先 `grid_range` 建基线，之后只应用
`revisions_since(version)` 即可恢复完整网格（见 `demo.py` 第 2 节）。

## 分块等价性

最终状态只取决于样本多重集合与同一时刻样本间的到达顺序，与分块方式
无关。`compute_reference` 以整段计算为参考，`test_resampler.py` 用
随机分块 + 随机撤回（两种模式各 30 轮）验证结果逐点一致。

## 保留历史样本与释放封闭区间的规则

- 未封闭前：保留全部历史，包括已撤回样本（其修订需可追溯）和被同
  时刻后到样本压盖的样本（撤回后需 resurfacing）。
- `close_until(t)` 声明 `(-∞, t]` 封闭（调用方承诺不再有 t 及之前的
  迟到/撤回），此后 t 及之前的变更一律拒绝。封闭时释放：
  - 锚点（≤ t 的最近有效样本）之前的所有样本——未来任何网格点的
    左邻不会越过锚点；
  - 锚点处被压盖/已撤回的样本；
  - ≤ 锚点时刻的断点——它们不可能再阻挡锚点之后的推导；
  - 网格时间 ≤ t 的修订事件与已发出缓存。
- 锚点样本本身必须保留（step 的携带值、linear 的左端点都依赖它），
  且因处于封闭区间内而不可再被撤回。

## 运行

```
python3 -m unittest test_resampler -v
python3 demo.py
```
