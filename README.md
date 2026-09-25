# gaprepair — 缺测区修复（线性 / 自回归，在线 / 离线）

纯 Python 3.11 标准库实现，无任何第三方依赖，不接音频设备；
样本数组与采样率由 `fixtures/` 下的本地 JSON 夹具提供（`null` 表示缺失）。

## 运行

```bash
python3.11 tools/make_fixtures.py   # 重新生成本地夹具（确定性）
python3.11 run_example.py           # 运行样例
python3.11 -m unittest discover -s tests -v   # 测试
```

## 规则

- 仅修复长度 `<= RepairConfig.max_gap` 的缺测区；更长的缺口保留未知
  （掩码 `MASK_UNKNOWN=2`，样本仍为 NaN）。
- 掩码：`0=已知, 1=已修复, 2=未知`；每个缺口附带逐点误差估计 `stderr`。
- **在线模式（online）**：只用缺口**之前**的已确认样本做自回归预测，
  绝不使用未来真值；开头缺口无历史 → 保留未知。
- **离线模式（offline）**：可单独使用两侧数据 —— 双锚点用双端线性插值；
  开头缺口无左锚时**禁止**双端线性插值，退化为右侧数据的反向 AR；
  末尾缺口用前向 AR。两种模式的结果带 `mode` 标签，不可混称。
- AR 拟合病态（法方程近奇异，主元比 < `CONDITION_TOLERANCE`）时逐阶降级，
  最终退化为均值，全部尝试记录在 `evidence["fit"]["attempts"]`。
- 已修复样本不会再被当作真实训练观测：每段修复只读取原始确认掩码，
  因此逐段恢复与整批恢复结果完全一致。
- 真实零值不是缺失（只有 NaN 算缺失）；已知样本绝不被修复过程改动。
- 置信范围通过合成遮挡实验校准（`synthetic_occlusion_experiment`）：
  人为遮挡已知样本、修复、与真值比较，得到 z 分数的 RMS 作为缩放因子，
  并报告 95% 经验覆盖率。
- 模型版本注册表支持撤回：`ModelRegistry.rollback()` 回到上一版本，
  或 `rollback(name)` 回到指定版本。

## 声明

- 数值容差：`NUMERIC_TOLERANCE = 1e-9`（`gaprepair/constants.py`）。
- 在线延迟：`gap_length + 1` 个样本（需等到缺口后首个确认样本到达才能
  识别缺口结束）；离线模式无此延迟约束。

## 结构

- `gaprepair/gaps.py` — 缺口检测（NaN 游程）
- `gaprepair/ar.py` — AR 最小二乘拟合、病态降级、psi 权重误差传播
- `gaprepair/repair.py` — 修复引擎（在线/离线、掩码、误差估计）
- `gaprepair/calibrate.py` — 合成遮挡校准实验
- `gaprepair/registry.py` — 模型版本注册与撤回
- `gaprepair/fixtures.py` — 本地夹具加载
- `tools/make_fixtures.py` — 夹具生成器
- `run_example.py` — 运行样例
- `tests/test_gaprepair.py` — unittest 测试套件
