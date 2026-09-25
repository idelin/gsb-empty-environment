# specband — 频谱到可变频带集合的积分

Python 3.11，仅标准库；测试使用 `unittest`。不接音频设备，样本数组与采样率
由本地夹具 `specband/fixtures.py` 提供（确定性合成正弦混合）。

## 公开频点覆盖模型（rect-bins/1.0）

- 由 `n_samples` 个时域样本、采样率 `sample_rate` 得到的单边功率谱，
  频点宽度 `df = sample_rate / n_samples`。
- 频点 `k` 拥有半开区间 `[k*df, (k+1)*df)`。
- 频带 `[lo, hi)` 对频点 `k` 的权重 = 区间交集长度 / `df`，取值 `[0, 1]`；
  频带边界可穿过频点，产生部分权重。

## 输出语义

每个频带返回：绝对能量、相对能量（占全谱总能量比例；总能量为零时为
`None`，表示未知）、重复覆盖贡献（落在被 ≥2 个频带覆盖的频点上的能量）。
频带可嵌套重叠；重叠带的能量**不做归一化**，其相对能量之和可以大于 1，
重叠部分通过 `repeated_coverage_energy` 与结果级 `shared_energy` 显式报告。

## 版本与更正

- 频谱帧：`SpectrumFrame(seq, version, samples, sample_rate)`，同 `seq`
  更高 `version` 视为更正；版本回退会被拒绝。
- 频带规则：`BandSet(version, bands)` 不可变，`Engine.switch_band_set`
  切换版本。
- 每条积分结果都打上 `(frame_version, band_version)` 标签，历史结果不
  重打标签；帧更正的增量 delta 始终在**当前生效的同一频带版本**下对新旧
  两帧重算，绝不跨版本混算，并附 `provenance` 来源证据。
- `Engine.checkpoint()` / `Engine.restore()` 通过事件日志序列化与重放，
  恢复后状态与继续推进行为一致；`missing_intervals()` /
  `missing_stats()` 给出缺帧区间统计。

## 声明的延迟与数值容差

- `DECLARED_FRAME_LATENCY_MS = 2.0`：夹具网格（N=32，朴素 O(N²) DFT +
  O(频带×频点) 加权）下单帧积分预算；实测约 0.02 ms（见 RESULTS.md）。
- `NUMERICAL_TOLERANCE = 1e-9`：能量比较绝对容差；累加使用 `math.fsum`。

## 运行测试

```sh
python3.11 -m unittest discover -s tests -t . -v
```
