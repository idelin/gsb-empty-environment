# 实际运行结果

环境：Python 3.11.15（macOS, arm64），仅标准库。
命令：`python3.11 -m unittest discover -s tests -t . -v`
日期：2026-09-26

```
test_checkpoint_restore (tests.test_specband.CheckpointTest.test_checkpoint_restore) ... ok
test_declared_latency_budget (tests.test_specband.EnergyConservationTest.test_declared_latency_budget) ... ok
test_energy_conservation_full_coverage (tests.test_specband.EnergyConservationTest.test_energy_conservation_full_coverage) ... ok
test_small_grid_manual_integration (tests.test_specband.ManualGridTest.test_small_grid_manual_integration) ... ok
test_missing_intervals_and_stats (tests.test_specband.MissingFramesTest.test_missing_intervals_and_stats) ... ok
test_no_missing (tests.test_specband.MissingFramesTest.test_no_missing) ... ok
test_band_widening_only_affects_related (tests.test_specband.PartialWeightTest.test_band_widening_only_affects_related) ... ok
test_partial_weights_boundary_bins (tests.test_specband.PartialWeightTest.test_partial_weights_boundary_bins) ... ok
test_zero_total_energy_relative_unknown (tests.test_specband.PartialWeightTest.test_zero_total_energy_relative_unknown) ... ok
test_correction_across_band_switch_no_mixing (tests.test_specband.VersionInterleavingTest.test_correction_across_band_switch_no_mixing) ... ok
test_version_regression_rejected (tests.test_specband.VersionInterleavingTest.test_version_regression_rejected) ... ok

----------------------------------------------------------------------
Ran 11 tests in 0.015s

OK

[latency] one frame integrated in 0.016 ms (declared budget 2.0 ms)
```

## 关键数值对照（测试断言依据）

- 能量守恒：夹具 SAMPLES_CALM（8 Hz 幅 1.0 + 16 Hz 幅 0.5，N=32, fs=64）
  总能量 0.625 = 0.5 + 0.125（正弦单边功率 A²/2），不重叠全覆盖三带
  之和与总能量差 < 1e-9，相对能量之和 = 1。
- 部分权重：df=2 Hz，带 [3,7) 切过频点 1 与 3，权重 0.5/1.0/0.5，
  手算能量 9.0 与积分结果一致；单带改宽 [3,7)→[3,11) 仅该带
  9.0→19.0，B、C 两带结果逐位相等。
- 重叠不归一：小网格（df=1 Hz，3 频点，谱 [1,2,4]）带 P=[0.5,2.5)、
  Q=[1.5,3) 的相对能量 4.5/7 + 5/7 > 1，断言成立；重复覆盖贡献
  P=4.0、Q=5.0、shared=6.0。
- 零总能量：SAMPLES_SILENT 相对能量为 None（未知），非 0/NaN。
- 版本交错：bands-A 下收帧 v1 → 切 bands-B → 收更正帧 v2；更正 delta
  完全在 bands-B 下对新旧帧重算（低频带 delta=0.105=(1.1²-1.0²)/2），
  历史结果保持 (v1, bands-A)，无混版本。
- 缺帧统计：seq {0,1,4,7} → 缺帧区间 [(2,3),(5,6)]，缺 4 帧，最长 2，
  覆盖率 0.5。
- checkpoint：事件日志 JSON 序列化→重放后 snapshot 完全一致，缺帧统计
  与更正记录保留，恢复后继续推帧与原引擎行为一致。
