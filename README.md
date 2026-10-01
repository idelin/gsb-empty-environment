# STFT 管线（变 hop / 帧丢失 / 局部频谱编辑）

纯 Python 3.11 标准库实现，无第三方依赖，不接音频设备；
样本数组与采样率由本地夹具 `fixtures.py` 提供。

## 声明

- **算法延迟**：一个完整窗长 `win` 个样本（`win / sample_rate` 秒）。
  需收齐一窗样本才能给出首个输出；代码中见
  `StftPipeline.algorithmic_latency_samples` / `latency_seconds`。
  编辑/删帧/补帧的增量重建延迟为 O(win)，仅触及受影响区间。
- **数值容差**：往返重建声明容差 `ROUNDTRIP_TOL = 1e-9`（`stft.py`），
  纯 Python float64 FFT 实测约 1e-15~1e-12；权重判零阈值
  `WEIGHT_EPS = 1e-12`，共轭对称校验容差 `HERM_TOL = 1e-9`（`pipeline.py`）。

## 设计要点

- **每样本实际权重归一**：逆变换按 `y[n] = Σ x_f[n]·w / Σ w²` 逐样本归一，
  权重来自帧网格的真实覆盖，绝不使用固定 COLA 系数——变 hop、短尾帧、
  删帧后权重均按实际重算（测试断言变 hop 下权重非常数）。
- **帧丢失**：`delete_frame` 后零权样本输出 `None` 并置不可恢复掩码，
  绝不补 0 冒充无损；`is_lossless()` 如实报告。
- **局部频谱编辑**：`edit_spectrum` 支持 `mirror`（自动补共轭）与
  `strict`（缺共轭对即拒绝）两种模式；DC/Nyquist 必须保持实数，
  违反实信号共轭约束的编辑抛出 `SpectralEditError`。
- **增量重建**：编辑/删帧/补帧只重建受影响区间 `[start, start+win)`，
  区间外输出逐样本不变（有测试保证）。
- **多通道时间对齐**：所有通道共享同一帧网格与掩码，构造时校验等长。
- **Checkpoint**：`checkpoint()` 保留未封闭重叠尾（尾部累加器与权重），
  `[0, seal)` 为已封闭输出，`from_checkpoint` 续跑与连续运行一致。
- **批量更正原子性**：`apply_batch` 任一操作失败即整体回滚，
  已发布版本（`publish`）不受影响。

## 文件

- `stft.py` — 窗函数、直接 DFT 参考、基-2 FFT（非 2 幂回退 DFT）、分帧起点
- `pipeline.py` — `StftPipeline` 管线主体
- `fixtures.py` — 确定性样本数组与采样率
- `examples.py` — 可重放样例（8 个场景）
- `test_stft.py` — unittest 套件（20 个用例）

## 运行

```sh
python3.11 -m unittest -v   # 测试
python3.11 examples.py      # 可重放样例，退出码 0 = 全部通过
```

## 测试结果

```
Ran 20 tests in 0.348s
OK
```

样例输出（可复现）：

```
[1] 固定合法窗/hop 往返: win=64 hop=16 max_err=1.332e-15 tol=1e-09 latency=64样本(8.00ms) -> PASS
[2] 变 hop 往返: 每样本权重取值数=123(非常数,禁用固定COLA) max_err=1.332e-15 -> PASS
[3] 删帧: 无法恢复区=[64,80) 输出=None(非补0冒充无损) 其余样本精确 -> PASS
[4] 补帧修复: 掩码恢复至删帧前状态 max_err=1.998e-14 -> PASS
[5] 局部频谱编辑: 仅重建区间[64,128) 区外逐样本不变 非对称编辑被明确拒绝 -> PASS
[6] checkpoint 续跑: 未封闭重叠尾(seal=112)保留 续跑结果与连续运行一致 -> PASS
[7] 批量更正原子性: 失败批次已回滚 发布版本保持v1 -> PASS
[8] 多通道时间对齐: 共享帧网格/掩码 通道0编辑不影响通道1 max_err=1.332e-15 -> PASS
ALL SCENARIOS PASSED
```
