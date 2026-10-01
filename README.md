# randomized-response

Python 3.11 实现二元与 k 元随机响应（Randomized Response, RR）的模拟与聚合估计。
**仅使用标准库**；测试基于 `unittest`。**只处理合成数据**。

## 定义与参数

- **字母表大小 `k`**（整数，`k >= 1`）：真实值与报告值均取自 `{0, ..., k-1}`。
  - `k = 2` 即经典二元随机响应（Warner 式）。
  - `k = 1` 为退化特例：字母表只有一个值，通道矩阵为 `[[1]]`，输出不含任何信息，
    `epsilon` 无意义（仍要求 `>= 0`），总体分布平凡地为 `[1.0]`。
- **隐私参数 `epsilon`**（实数，`epsilon >= 0`，有限）：对称 k 元 RR 机制为
  - `p_stay = e^eps / (e^eps + k - 1)`（如实报告），
  - `q = 1 / (e^eps + k - 1)`（报告其余每个值）。
- **响应通道矩阵 `A`**：`A[y][x] = P(报告 y | 真实 x)`，列和为 1，
  `A = (p_stay - q) I + q J`。
- **`epsilon = 0`**：`p_stay = q = 1/k`，输出与输入独立（均匀分布），通道秩为 1，
  总体分布**不可辨识**，本库不会强行求逆，而是报告不可辨识方向并给出最小范数解。

> 该机制广为人知地满足 epsilon-差分隐私，但本库只做统计算法实现；
> 其中的模拟实验**不构成任何形式化隐私证明**。

## 功能

- 参数合法性检查（`epsilon`、`k`、分布、计数的类型与取值）。
- 合成数据模拟：`simulate_counts`；理论输出分布 `theoretical_output_distribution`
  （模拟频率在大样本下接近理论概率，见测试）。
- 聚合估计 `estimate_distribution`：
  - 允许不同参与者使用**不同的已知通道矩阵**，按通道分组（`ChannelGroup`）
    通过加权最小二乘 `min sum_g n_g ||A_g x - f_g||^2` **联合恢复**总体分布；
  - 基于法方程矩阵 `M = sum_g n_g A_g^T A_g` 的特征分解识别**不可辨识方向**
    （零空间），仅在可辨识子空间上做伪逆，绝不强行求逆；
  - 原始（无约束）估计**可以超出** `[0,1]` 或不归一，原样报告于 `raw_estimate`；
    满足非负且总和为一的约束估计通过对概率单纯形的欧氏投影得到，
    **单独**列于 `estimate`，被裁剪坐标列于 `clipped`；
  - 诊断量：加权 RMS 残差 `residual`、可辨识子空间上的条件数 `condition_number`。
- 置信分析 `confidence_intervals`（Wald 区间，基于三明治协方差 `M^+ G M^+`）：
  - 覆盖假设（见 `ConfidenceReport.assumptions`）：i.i.d. 抽样、通道已知、
    多项分布计数代入观测频率、原始线性估计量的正态近似——仅为渐近覆盖，
    小样本或边界概率下不可靠；区间针对**未裁剪**估计量；
  - 退化通道限制：联合系统不可辨识（如所有组 `epsilon = 0`，或 `k = 1`）时
    不存在有限区间，直接报错而非编造。
- 小域精确参考 `exact_binary_reference`：二元 RR 下报告 0 的计数恰为二项分布，
  枚举原始/裁剪估计量的**精确**分布，用于检验偏差（原始估计精确无偏；
  裁剪估计在边界处有精确可算的小偏差）。

## 运行

```bash
python3.11 examples/run_examples.py   # 运行样例
python3.11 -m unittest discover       # 运行测试（39 个用例）
```

测试结果：`Ran 39 tests ... OK`（Python 3.11）。

## 代码结构

- `randomized_response/channels.py` — 通道定义与参数校验
- `randomized_response/estimation.py` — 分组联合估计、可辨识性、投影、诊断
- `randomized_response/inference.py` — 置信区间与覆盖假设说明
- `randomized_response/simulation.py` — 合成数据模拟与精确参考分布
- `randomized_response/linalg.py` — 纯 Python 线性代数（消元、Jacobi 特征分解、单纯形投影）
- `tests/` — unittest 测试套件
- `examples/run_examples.py` — 六个运行样例
