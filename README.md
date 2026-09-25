# 频谱到可变频带集合积分器

本项目使用 Python 3.11 标准库实现离线频谱积分。样本数组和采样率只来自本地测试夹具，不打开、不枚举、不依赖任何音频设备。

## 频谱与覆盖模型

- DFT 模型版本：`real-dft-energy-v1`。
- 输入帧为实数样本，帧长 `N` 必须为不小于 2 的偶数，频率分辨率为 `df = sample_rate / N`。
- 单边频点为 `0, df, ..., sample_rate / 2`。
- 能量谱密度满足 Parseval 守恒：DC 和 Nyquist 频点为 `|X[k]|^2 / sample_rate`，内部频点为 `2 * |X[k]|^2 / sample_rate`；全频段积分等于时域能量 `sum(x[n]^2)`。
- 频点覆盖模型版本：`dft-voronoi-v1`。
- 每个频点拥有不重叠的 Voronoi 覆盖单元：DC 为 `[0, df/2]`，内部频点为 `[f-df/2, f+df/2]`，Nyquist 为 `[f-df/2, f]`。
- 频带边界可以穿过频点；频带获得的频点权重为“频带与覆盖单元的重叠长度 / 覆盖单元宽度”。边界正好落在频点时，该频点权重为 `0.5`。

## 输出语义

每个频带返回：

- `absolute_energy`：该频带的绝对能量。
- `relative_energy`：该频带能量 / 当前帧总能量；总能量为零时为 `None`，表示未知。
- `coverage_weights`：该频带对每个公开 DFT 频点的覆盖权重。

每个帧事件还返回：

- `total_energy`：当前帧总能量。
- `gross_coverage_energy`：所有频带绝对能量的直接总和，允许因重叠超过总能量。
- `union_energy`：合并嵌套和重叠区间后的并集能量。
- `duplicate_coverage_energy`：`gross_coverage_energy - union_energy`，即重复覆盖贡献。
- `duplicate_relative_energy`：重复覆盖贡献 / 当前帧总能量；总能量为零时为 `None`。

重叠或嵌套频带不会被错误归一化为 1；只有不重叠且完整覆盖 `[0, sample_rate/2]` 的频带集合，其相对能量总和才应为 1。

## 版本、更正与证据

- 频谱帧通过 `frame_version` 标识版本；`correct_frame()` 按 `frame_id` 追加更正事件，不重写历史事件。
- 频带规则通过 `band_version` 和 `effective_from_sample` 切换；帧按 `start_sample` 选择当时有效的频带版本。
- 频带版本切换后，迟到的旧帧更正仍使用该旧帧样本时间对应的频带版本，不会混用新版本。
- 频带版本切换会影响已存在的相关帧时，处理器追加带增量的事件，而不是修改旧事件。
- 更正事件包含每带绝对能量增量、每带相对能量增量、总能量增量、重复覆盖能量增量和更正原因。
- 每个事件包含来源证据：帧 ID、帧版本、样本区间、采样率、帧长、帧 SHA-256、频带版本、频带规则 SHA-256、DFT 模型版本、覆盖模型版本、频率分辨率和频点数量。

## 缺帧与 checkpoint

- `missing_intervals()` 按帧起点网格统计缺失区间，返回区间起止样本、长度和缺失帧起点。
- `save_checkpoint()` 以 JSON 原子写入完整状态；`load_checkpoint()` 恢复帧、事件日志、频带版本时间线、最新结果和缺帧状态。
- checkpoint 状态格式版本为 `spectrum-integrator-state-v1`。

## 延迟与数值容差

- 本实现是帧对齐的离线/批量处理器，不访问音频设备，因此没有设备采集延迟或额外音频缓冲延迟。
- 一帧可积分的算法延迟为一个帧长：`frame_length / sample_rate` 秒；测试夹具为 `4 / 4 = 1` 秒。
- 声明的绝对能量容差：`1e-12`。
- 声明的相对能量容差：`1e-12`。
- 测试中的浮点对照使用 `unittest.TestCase.assertAlmostEqual` 的默认 7 位小数精度；接近零的重复覆盖误差按绝对容差归零。

## 运行测试

```bash
python3.11 -m unittest discover -s tests -v
```

实际运行记录保存在 `test-results.txt`。
