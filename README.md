# clockmap — 从样本序号恢复墙钟时间

纯 Python 3.11 标准库实现（仅 `fractions` / `dataclasses` / `unittest`），
从**样本序号 + 名义采样率 + 校准点**恢复墙钟时间，输出**分段可行区间映射**，
绝不假设均匀绝对时钟。

## 设计要点

- **精确表示**：所有时间量都是 `fractions.Fraction`，`float` 一律拒绝。
  2**60 秒附近相差 2**-30 秒的两个时间戳不会被 float64 吞掉。
- **类型区分**：`WallTime`（墙钟时间点）、`Duration`（持续时长）、
  `TimeInterval`（时间点闭区间）。时间点 + 时间点直接 `TypeError`。
- **斜率界（漂移）**：`SlopeModel` 给出每样本墙钟周期界
  `[min_period, max_period]`（`from_ppm` 由名义采样率 ±ppm 构造）。
- **校准点给区间**：`Anchor(epoch, seq, wall_lo, wall_hi)` 把序号钉在
  误差区间上，而非精确点。
- **可行区间**：某序号的可行墙钟区间 = 所有锚点各自外推区间的交集
  （前向用 `[lo+Δ·min, hi+Δ·max]`，后向对称）。交集为空即矛盾。
- **最小锚点冲突集**：区间在直线上，不可行必有一对锚点作证
  （自身矛盾的锚点则是单元素集），`minimal_conflict_set()` 直接返回。
  不一致的校准点被标记为异常（`is_anomalous`），但保留为证据。
- **epoch 不可跨接**：锚点与查询都带 `epoch`；跨 epoch 求时长抛
  `EpochBoundaryError`；时钟重置后的新序号绝不接旧序号。
- **丢样**：序号跳 10 表示 10 个采样周期仍然流逝，
  `duration_between` 只依赖序号距离，与连续性无关。
- **迟到校准**：`map_seq` 每次发出一个 `Estimate` 版本；新锚点到达后
  重新查询得到修订版，旧结果被标记 `superseded` 并保留在证据链中
  （`estimates()` 返回完整修订历史）。
- **合成偏移转换表**：`OffsetTable` 在控制点存精确偏移区间
  `offset = wall - seq·nominal_period`，线性插值全程有理数，
  支持 `to_wall` / `to_seq` 双向精确转换。

## 运行

```sh
python3.11 -m unittest discover -v   # 测试
python3.11 examples/demo.py          # 运行样例
```

## 文件

- `clockmap/timeprims.py` — `WallTime` / `Duration` / `TimeInterval`
- `clockmap/model.py` — `SlopeModel` / `Anchor`
- `clockmap/mapper.py` — `ClockMapper`（可行区间、冲突、修订）
- `clockmap/offset_table.py` — `OffsetTable`
- `tests/test_clockmap.py` — 小有理域手工核验的参考测试
- `examples/demo.py` — 端到端场景演示
- `RESULTS.md` — 测试与样例的实际输出
