# gaprepair — 缺测区修复（Python 3.11，仅标准库）

对均匀采样信号中的**短缺测区**（NaN 段）做修复：在线模式用因果自回归（AR）预测，
离线模式用双端线性插值。输出修复掩码与逐样本误差估计；超过阈值的长缺口保留未知。

## 声明

- **环境**：Python 3.11，仅标准库；测试用 `unittest`。
- **数据**：样本数组与采样率（8000 Hz）由本地夹具 `gaprepair/fixtures.py` 确定性生成，
  **不接触任何音频设备**。
- **缺失表示**：仅 NaN 视为缺失；真实的 `0.0` 是有效观测，绝不当作缺失。
- **数值容差**：`NUMERICAL_TOLERANCE = 1e-9`（确定性/一致性判定的容差）。
- **延迟**：
  - 在线（online）：缺口闭合（缺口后第一个确认样本到达）时即发出修复，**绝不使用缺口之后的样本**；
  - 离线（offline）：需等待缺口两侧锚点都观测到，可使用缺口两侧数据。
  - 两种模式语义不同，不可混称；每段修复的 `evidence["mode"]` 记录了实际所用模式。

## 行为规则

- 缺口长度 `> max_gap_len`（默认 8）→ 保留未知（`left_unknown`），证据中给出原因。
- 在线 AR 只用缺口**之前**的**确认样本**训练；已修复样本永远不会成为后续训练观测
  （`train_samples_used` 可验证）；预测上下文可含先前修复值（确定性），但不参与训练。
- 病态拟合（训练样本不足、近常数段、法方程奇异/病态）→ 降级为双端线性插值；
  无锚点可降级时保留未知。降级原因与条件数估计写入 `evidence`。
- 开头缺口无左锚 → 不做双端线性插值（离线保留未知）；在线无历史同样保留未知。
- 已知样本绝不被修复过程改动；输入列表不被原地修改。
- 逐段恢复（`restore_segments`）与一次性恢复（`repair_all`）结果一致（容差 1e-9 内，
  实测逐位相同）。

## 置信范围校准与版本撤回

- `synthetic_occlusion_experiment`：在干净信号上合成遮挡、修复、与真值比对，
  计算置信区间（`confidence_z`，默认 1.96）的经验覆盖率。
- `calibrate_error_scale`：按目标覆盖率（默认 0.95）取 |误差|/估计 的分位数求刻度。
- `ModelRegistry`：注册模型版本；`calibrate_and_register` 在验证集上比上一活跃版本
  更差时自动撤回（rollback）；也支持显式 `rollback()` / `rollback(name)`。

## 运行

```bash
python3.11 main.py                      # 演示：修复 + 校准 + 版本撤回
python3.11 -m unittest discover -v      # 测试（28 个用例）
```

## 运行样例（节选）

```
== online repair (causal AR, degrades to linear) ==
samples=400 repaired=13 unknown=1
  gap start= 40 len= 3 status=repaired     method=ar     err_est=[0.0460, 0.0831, 0.1238]
  gap start=120 len= 6 status=repaired     method=ar     err_est=[0.1599, ..., 0.4240]
  gap start=200 len=12 status=left_unknown method=None   err_est=[none]
      evidence={'reason': 'gap length 12 exceeds max_gap_len 8'}
  gap start=300 len= 4 status=repaired     method=ar     err_est=[0.1614, ..., 0.3522]

== edge gaps [(0, 4), (397, 3)]: leading gap has no left anchor ==
  online  gap start=  0 len=4 status=left_unknown evidence=insufficient confirmed samples for AR fit
  offline gap start=  0 len=4 status=left_unknown evidence=two-sided linear interpolation requires both anchors...

segment-by-segment restore matches repair_all: max diff 0.000e+00 (tolerance 1e-09)

== synthetic occlusion calibration + version rollback ==
  v0 val coverage (z=1.96): 0.979
  calibrated scale: 1.1566
  v1 train coverage: 0.958 val coverage: 0.979
  active version: calibrated-v1 (rolled back: False)
  after explicit rollback: uncalibrated-v0
```

## 测试结果

```
Ran 28 tests in 0.5s
OK
```

覆盖：缺口检测、真实零值、阈值边界、长缺口保留、已知样本不变、输入不被修改、
在线无未来泄漏（改动缺口后样本修复结果不变）、离线使用未来数据（两模式区分）、
已修复样本不入训练、病态/奇异拟合降级并带证据、开头/结尾缺口处理、双端线性精确性、
掩码与误差对齐、逐段恢复一致性、重复运行确定性、校准达标、注册表撤回、
差版本自动撤回、夹具完整性。

## 文件

- `gaprepair/core.py` — 缺口检测、AR 拟合/预测、线性插值、修复主流程、逐段恢复
- `gaprepair/calibration.py` — 合成遮挡实验、误差刻度校准、模型注册表与撤回
- `gaprepair/fixtures.py` — 本地确定性夹具（样本数组 + 采样率）
- `main.py` — 演示入口
- `test_gaprepair.py` — unittest 测试套件
