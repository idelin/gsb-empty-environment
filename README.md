# 流式滤波器验证器（Python 3.11，仅标准库）

对**未知的、可流式调用的**滤波器实现做黑盒验证：脉冲、扫频、多音测试，
计算通带偏差、阻带泄漏与时间偏移；自动搜索分块不一致与相位错位反例并缩减；
频响误差与有限测试分辨率分开报告；对 FIR 可由系数给出频率网格间的保守误差界。
样本数组与采样率全部由本地确定性夹具提供，不接触任何音频设备。

## 目录结构

```
verifier/
  dsp.py         纯 Python 基-2 FFT / IFFT / 互相关
  windows.py     窗函数与按相干增益归一化的幅度谱
  signals.py     脉冲 / 对数扫频 / 多音 / 噪声 信号生成（全部确定性、可重放）
  metrics.py     正交投影测幅相、互相关测延迟、dB 换算
  spec.py        FilterSpec：明确频段、声明延迟与全部数值容差
  streaming.py   流式执行器、分块/移位反例搜索、可重放反例（JSON）
  shrink.py      反例缩减（前缀截断 + ddmin 去边界 + 边界位置最小化）
  fir_bounds.py  FIR 系数 DTFT + Lipschitz 保守网格间误差界
  reference.py   参考 StreamingFIR 与窗函数法低通设计
  bad_impls.py   注入用的已知坏实现
  fixtures.py    本地夹具：样本数组 + 采样率，种子可重放
  core.py        verify() 编排全部检查
  report.py      报告：每项检查含实际样本数、测量方法、容差
examples/run_example.py   运行样例
tests/test_verifier.py    unittest 套件（23 项）
```

## 约定

- 频率 Hz，时间样本，幅度为实正弦的线性峰值；dB = 20·log10（幅度比）。
- 通带：|H| 相对单位增益偏差 ≤ `passband_ripple_db`；阻带：|H| 衰减 ≥ `stopband_atten_db`。
- **归一化**：加窗谱按窗的*相干增益* `sum(w)/N` 归一化，绝不使用窗能量
  `sum(w^2)`——否则单位增益会被误判为衰减（Hann 窗会伪造约 4.2 dB 损失）。
  多音测量使用无窗正交投影，单位增益精确读为 0 dB（有测试守护）。
- **近 Nyquist**：测试音限制在 `fs/2 - nyquist_guard_hz` 以下并吸附到分析段
  bin 上；投影按真实频率进行，不做负频率混叠解释。恰好在 fs/2 处正弦分量
  消失，幅度改由余弦投影恢复（不乘 2，与 DC 同理），相位报告为 None。
- **流式契约**（被测实现需满足）：
  `reset()`；`process(chunk)` 返回恰好 `len(chunk)` 个样本；
  `finalize()` 返回尾部样本（可为空）；`latency_samples` 为声明延迟。
  验证器只接触工厂函数，可反复生成全新实例探测未知实现。

## 检查项与测量方法

| 检查 | 方法 |
|---|---|
| `impulse_response` | 脉冲激励；互相关峰值 + 抛物线插值测延迟；-120 dB 支撑长度 |
| `declared_latency` | 实测延迟 vs `latency_samples`，容差 `latency_tolerance_samples` |
| `swept_response` | 对数扫频解卷积 H=Y/X（仅幅度，与延迟无关），网格采样 |
| `multitone_passband` | bin 对齐多音 + 稳态段正交投影，通带偏差 dB |
| `stopband_leakage` | 阻带多音，输出/输入幅度比，泄漏 dB |
| `nyquist_edge` | 近 Nyquist 三个边音的衰减 |
| `chunk_invariance` | 随机分块（2–8 块）对照单块参考，超限则搜索出反例并缩减 |
| `shift_invariance` | 输入移位 1..k，输出应为参考的移位；升序首个失败即最小移位 |
| `fir_certificate` | 系数 DTFT 自适应加密网格 + Lipschitz 界，覆盖网格间全部频率 |

**误差与分辨率分开报告**：`swept_response` 同时给出测得的纹波/衰减与
`grid_spacing_hz`，并明确声明网格间行为不由该测量保证；`fir_certificate`
用 `L = (2π/fs)·Σn·|h[n]|` 的 Lipschitz 界在网格区间 `[min-L·d/2, max+L·d/2]`
上保守认证，输出 certified / violated / undetermined。绝不靠少量扫频点
声称全频段达标。

**反例与证据**：分块/移位反例含完整输入样本、分块边界、最大偏差与容差，
以 JSON 保存（`artifacts/`），`ChunkCounterexample.load(...).still_fails(factory)`
可随时重放。缩减后反例可小至 2 个样本、1 条边界。

## 运行

```bash
python3.11 -m unittest discover -v     # 测试
python3.11 -m examples.run_example     # 运行样例（写入 artifacts/）
```

## 运行样例（实际输出，节选）

```
GOOD IMPLEMENTATION: 101-tap blackman lowpass, fc = 10 kHz, fs = 48 kHz
verdict: PASS (9/9 checks)
[PASS] impulse_response     n=2048    delay_samples=50, response_support=100
[PASS] declared_latency     n=2048    measured_delay=50, declared_latency=50
[PASS] swept_response       n=8192    passband_ripple_db=0.001323, stopband_atten_db=76.48,
                                      grid_spacing_hz=2.93
[PASS] multitone_passband   n=4096    deviation_db=0.0007429, tones=8
[PASS] stopband_leakage     n=4096    leakage_db=-87.11, tones=8
[PASS] nyquist_edge         n=4096    worst_leakage_db=-114.9
[PASS] chunk_invariance     n=1024    max_abs_diff=0
[PASS] shift_invariance     n=1024    max_abs_diff=0
[PASS] fir_certificate      n=101     passband_status=certified, stopband_status=certified,
                                      stopband_interval_hz=1.465, lipschitz_per_hz=0.01316

INJECTED KNOWN-BAD IMPLEMENTATIONS (detection check)
  state_reset          DETECTED  failed checks: chunk_invariance
  chunk_delay          DETECTED  failed checks: chunk_invariance
  absolute_index       DETECTED  failed checks: swept_response, shift_invariance
  gain_error           DETECTED  failed checks: swept_response, multitone_passband
  latency_liar         DETECTED  failed checks: declared_latency
  underspec_stopband   DETECTED  failed checks: swept_response, multitone_passband, stopband_leakage
  ripply_passband      DETECTED  failed checks: swept_response

REPLAY OF SHRUNK COUNTEREXAMPLES
  chunk counterexample: input=2 samples, boundaries=[1], tol=1e-09
    replay vs StateResetFIR : still_fails=True  max_abs_diff=2.62e-07
    replay vs good filter   : still_fails=False max_abs_diff=0
  shift counterexample: shift=1 sample(s), input=1024 samples
    replay vs AbsoluteIndexFIR: still_fails=True max_abs_diff=0.169
```

## 测试结果

```
$ python3.11 -m unittest discover
.......................
----------------------------------------------------------------------
Ran 23 tests in ~15s

OK
```

覆盖：FFT 正确性；窗相干增益归一化（单位增益不被误判为衰减）；正交投影
幅相恢复；近/恰好 Nyquist 音解释；群延迟估计；好实现分块/移位不变；
StateReset/ChunkDelay 分块反例检出并缩减（≤4 样本、1 边界）且可重放；
AbsoluteIndex 移位反例（最小移位 1）；Lipschitz 界保守性（稠密采样不越界）；
好 FIR 全频段 certified、欠规格 FIR violated；7 种坏实现全部被对应检查检出；
报告含实际样本数与方法且可 JSON 序列化；夹具保存/加载逐样本一致。
