# fractional-align：多通道整数/分数延迟对齐引擎

纯 Python 3.11 标准库实现（无第三方依赖，测试用 `unittest`）。样本数组、
采样率与分数延迟滤波系数全部由本地夹具提供，**不接触任何音频设备**。

## 信号模型

- 所有通道共享同一输出时间基准：输出位置 `n` 在各通道间对齐。
- 每通道一条分段线性延迟轨迹 `d(n)`（单位：样本），引擎在分数输入位置
  `p(n) = n - d(n)` 处用夹具提供的 FIR 系数求值：
  `y[n] = Σ_k h(μ)[k] · x[floor(p) - L/2 + 1 + k]`，其中 `μ = p - floor(p)`。
- 延迟平滑变更：每段斜率严格满足 `|dd/dn| < 1`，因此 `p(n)` 严格递增——
  不会突然重复或跳过未声明的样本；越界声明直接抛 `ValueError`。
- 延迟变化按样本位置驱动（knot 位于输出位置），未确认区间可撤回：
  `retract_delay_after()` 撤回未发射的未来变化，`checkpoint()/restore()`
  回滚已发射但未确认的区域；已 `commit()` 的历史被锁定。
- 通道间零串音：每通道独立的历史缓冲、延迟轨迹与缺测掩码。

## 输出

- `OutputBlock`：共同时间基准上的一个块，含每通道 `data` 与 `flags`。
- 补零标记：`flags & ZERO_PAD` —— 输入窗越界，越界部分按零处理。
- 缺测污染掩码：`flags & CONTAMINATED` —— FIR 窗与声明的缺测区间重叠，
  缺测样本按零参与卷积；掩码随 checkpoint 一起保存/恢复。
- `common_valid_interval()`：各通道有效区间的交集（共同有效区间）。

## Checkpoint 内容

输出游标、每通道滤波历史（环形缓冲）、缺测污染掩码、延迟轨迹快照及其
版本号（`DelaySchedule.version`，每次声明/撤回自增）。

## 内存上限

每通道保留历史不超过

    未确认跨度 U + ⌈max_delay⌉ + 核长 L

个样本，与流总长无关（推导：`p(n)` 单调，已确认游标之前的窗尾可丢弃；
未确认部分由 checkpoint 覆盖）。已喂入但尚未消耗的输入属于调用方队列，
不计入引擎保留历史。

## 参考校验

`fractional_align/reference.py` 为整段参考卷积（无流式状态）。测试用它
交叉校验：任意分块（1/3/16/97/511 等块大小输出位级一致）、尾部排空
（精确到最后一个有效输出）、变化重叠（斜坡中途撤回并重新声明）。

## 声明参数（夹具）

| 参数 | 值 |
| --- | --- |
| 采样率 | 48000 Hz |
| 分数延迟核长 | 32 taps（加窗 sinc，DC 归一化） |
| 最大延迟 | 64 样本 |
| 数值容差 | 1e-9（引擎 vs 参考卷积） |

## 运行

```sh
python3.11 -m unittest discover -s tests -v   # 测试
python3.11 examples/run_example.py            # 端到端样例
```

## 目录

- `fractional_align/fixture.py` —— 夹具：系数、信号、采样率、容差
- `fractional_align/schedule.py` —— 分段线性延迟轨迹（版本化、可撤回）
- `fractional_align/engine.py` —— 流式对齐引擎（checkpoint/commit/flush）
- `fractional_align/reference.py` —— 整段参考卷积
- `fractional_align/harness.py` —— 流式驱动辅助（测试与样例共用）
- `tests/test_engine.py` —— 19 项 unittest 校验
- `examples/run_example.py` —— 运行样例
