# 滑窗归一互相关多通道相对延迟估计

纯 Python 3.11 标准库实现。无第三方依赖、不接音频设备；样本数组与采样率由调用方（测试中为本地夹具 `tests/fixtures.py`）提供。

## 运行

```bash
PYTHONPATH=src python3.11 -m unittest -v tests.test_delay_estimation
```

## 延迟约定与声明的容差

- 约定：`b[n] ≈ polarity · a[n - d]`。`d > 0` 表示通道 `b` 相对 `a` 延迟；秒 = `d / fs`。
- 整网分辨率：1 个样本（`LAG_UNIT_SAMPLES = 1.0`），整网网格量化界 ±0.5 样本（`GRID_HALF_BIN_SAMPLES`）。
- 分数延迟：在整数峰 ±1 样本内以 0.05 步长重算掩码 NCC，取 `|NCC|` 最大点（`refine_fractional`）。默认报告的 1σ 量级不确定度 ≥ 0.5 样本；多峰并列为整数候选间隔。
- 漂移建模：速度定义在“样本/窗”的有限网格上（默认 `{-1, 0, 1}`），允许加速度变化；相邻落地窗之间按线性运动 `Δlag == v · span` 约束，并施加 `|Δlag| ≤ max_jump · span` 的硬界。
- 数值容差（`src/delayest/tolerances.py`）：`NCC_CLAMP_TOL=1e-9`、`PEAK_TIE_TOL=1e-9`、`MIN_PEAK_NCC=0.15`、`MIN_OVERLAP_PAIRS=8`、`FRAC_OFFSET_BOUND=0.75`。
- DP 代价尺度：`weight_corr=10`（发射代价 `10(1-|NCC|)`），加速度惩罚 `(Δv)^2`，拒绝有证据窗 `skip_cost=2`，跨缺口重启 `restart_cost=3`，未知窗穿越免费。

## 能力对应

- **缺测掩码**：仅使用两通道在该整数/分数延迟下的有效样本交集；交集对不足 `MIN_OVERLAP_PAIRS` 的窗口记 `insufficient_overlap`，保持未知。
- **极性翻转**：峰检测在 `|NCC|` 上进行并记录符号；`WindowDelay.polarity ∈ {+1, -1}`。
- **线性漂移**：`(lag, velocity)` 状态 + 加速度转移，随时间线性跟踪。
- **并列峰**：全局等高的局部极大全部保留为候选（`candidate_lags`、`ambiguous=True`），周期信号不会被冒称为唯一。
- **全时间轨迹约束**：有限候选动态规划在整个时间轴上取全局代价最小路径，不是每窗独立 argmax。
- **低信息窗口未知**：静音（零方差）、交集不足、`|NCC|` 过低均不落地。
- **迟到窗口修订**：`DelayEstimator` 可乱序/迟到添加窗口；每次 `solve()` 用全部证据重解，新证据可回溯改变前后路径。
- **异常峰有界**：异常峰若要求超过 `max_jump·span` 的跳变，将被拒绝（付 `skip_cost`），轨迹从其旁连续 carry，不产生无界跳变。
- **Checkpoint**：证据与参数 JSON 序列化到磁盘；恢复后继续添加窗口的结果与一次性连续运行逐值一致。

## 模块

- `src/delayest/ncc.py`：掩码交集 NCC、峰候选（含并列）、分数精修、窗口证据与状态分类。
- `src/delayest/dp.py`：有限候选动态规划（landed / carry / gap 状态）。
- `src/delayest/estimator.py`：流式 `DelayEstimator`、一次性 `estimate_delays`、不确定性、秒换算、checkpoint。
- `tests/fixtures.py`：本地合成长序列（分数时延 + 线性漂移 + 随机缺测 + 极性）、周期脉冲串、唯一峰噪声窗；`FS=100 Hz`。
