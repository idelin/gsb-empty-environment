# dyncurve — 动态曲线编译与处理（纯标准库，Python 3.11）

不依赖任何第三方包，不接触音频设备；样本数组与采样率由
`dyncurve/fixtures.py` 本地确定性生成。

## 结构

- `dyncurve/curve.py` — 静态曲线：硬拐点 / 软拐点 / 多段压缩与扩展的编译，
  编译期检查值连续、导数条件（软拐点处一阶导连续）与单调性；
  曲线为**纯无状态函数**，另提供逆查 `invert()` 与不可逆平台区间
  `plateau_regions()`。
- `dyncurve/envelope.py` — 包络检测（peak / RMS）与增益平滑
  （attack/release，dB 域），全部有状态部分集中于此，与静态曲线分离。
- `dyncurve/processor.py` — 多通道处理器：可配置 sidechain
  （`SidechainConfig.channels` 选择外部/本机通道，`link` 选择
  linked/independent），规则热更新 `update_curve()`。
- `dyncurve/fixtures.py` — 本地夹具：`SAMPLE_RATE = 48000`，正弦、阶跃、
  静音、噪声（固定种子）等样本数组。
- `examples/demo.py` — 可运行样例。
- `tests/` — unittest 套件（28 项）。

## 声明

- **总延迟**：`DynamicsProcessor.latency_samples == 0`（无 lookahead）；
  热更新不改变延迟。
- **数值容差**（`dyncurve/curve.py`）：`VALUE_TOL = 1e-9` dB（值连续）、
  `SLOPE_TOL = 1e-7` dB/dB（导数连续）、`INVERSE_TOL = 1e-9` dB（逆查）、
  `DB_FLOOR = -120 dB`（静音下限，0 幅值映射到此而非 -inf，
  静音永不产生无穷）。
- **比例 1 为恒等**：ratio = 1 的段贡献为零，输出严格等于输入。
- **软拐点**：二次插值，两端值与斜率均连续（编译期解析验证 +
  测试对照独立闭式参考）。
- **热更新**：`update_curve(curve, fade_samples, envelope_policy)` 在下一次
  `process_block` 调用的首个样本（既定切换点）开始交叉过渡，
  目标增益在 dB 域线性插值 `fade_samples` 个样本，无增益突跳、无延迟变化。
- **包络状态策略（明示）**：
  - `EnvelopePolicy.RETAIN`（默认）：检测器与平滑器状态全部保留，
    增益轨迹连续；
  - `EnvelopePolicy.RESET`：检测器状态重置为静音，但平滑器增益状态
    仍保留，保证外加增益不突跳。
  两种策略均有测试验证（`tests/test_processor.py::TestHotReload`）。
- **级联 ≠ 合并参数**：压缩与扩展级联的结果不等于把参数合并成单条曲线
  （`TestCascade` 验证二者差异 > 1 dB）。
- **分块不变性**：任意随机分块与整段处理产生逐样本完全相同的输出与
  最终状态（`TestBlockInvariance`）。

## 运行

```sh
python3.11 examples/demo.py                  # 可运行样例
python3.11 -m unittest discover -s tests -t . -v   # 测试（28 项）
```
