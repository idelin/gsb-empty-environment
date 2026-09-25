# kernel-switch：运行时滤波核切换（状态迁移 + 交叉淡化）

纯 Python 3.11 标准库实现，无第三方依赖，不接触音频设备；
样本数组与采样率由本地夹具 `fixtures.py` 提供。

## 设计要点

- **共享输入延迟线**：所有活跃核实例都是 FIR，且共享同一条输入历史
  （长度上界 = 版本图中最大核长）。新核切换即"状态迁移"——新核直接读取
  共享历史，等效于从流起点就在运行，因此常量信号在等增益核间切换无台阶。
- **核版本过渡图**：`TransitionGraph` 以有向边声明允许的切换及淡化时长；
  非法切换抛 `KeyError`。切换中的再次切换按规则处理：先把当前混合
  （含旧核尾响）**合成为单个等效核**（共享输入下 w1·h1+w2·h2 是精确等效，
  尾响不丢失），再从合成核向新目标淡化。
- **有界开销**：任意时刻活跃实例 ≤ 2，历史 ≤ 最大核长；
  每样本开销 O(2 × 最大核长)，工作内存 O(最大核长)。
- **采样点精确**：淡化调度使用绝对样本序号，块切分不影响切换采样点
  （测试为比特级一致）。
- **元数据**：每个输出样本携带来源核权重 `weights` 与精确样本时间
  `sample_index` / `time_seconds`。
- **快照/恢复**：任意样本边界都是安全点，`snapshot()` 产出可 JSON 序列化
  字典，`restore()` 后逐比特一致地继续处理。

## 声明

- **延迟**：0 个样本（严格因果，无前视；不补偿核本身的群延迟）。
- **数值容差**：全程 float64；参考卷积对比的绝对容差 1e-9；
  块切分一致性与快照恢复要求比特级相等（容差 0）。

## 文件

- `kernel_switch.py` — 核心库（KernelVersion / TransitionGraph / KernelSwitchFilter）
- `fixtures.py` — 本地样本夹具（常量/脉冲/正弦/定种子噪声，48 kHz）
- `demo.py` — 运行样例
- `test_kernel_switch.py` — unittest 套件（13 项）

## 运行

```
python3.11 demo.py
python3.11 -m unittest test_kernel_switch -v
```

## 运行样例输出（python3.11 demo.py）

```
sample rate: 48000 Hz | declared latency: 0 sample(s) | abs tolerance: 1e-09
constant-signal switch ma4->ma16: max |y-1| after fade = 0.000e+00
  meta @n=132 t=0.002750s weights={'ma4': 0.4844, 'ma16': 0.5156}
impulse @n=58, switch ma4->ma48 @n=60: y[58:70] = [0.25, 0.25, 0.246419, 0.242839, 0.000977, 0.001302, 0.001628, 0.001953, 0.002279, 0.002604, 0.00293, 0.003255]
cancel mid-fade @n=140: final weights = {'ma4': 1.0}
snapshot/restore at n=170 (mid-fade): bit-exact continuation = True
bounds: active instances <= 2, history <= 48 samples (max kernel length)
```

脉冲样例中可见旧核 ma4 的尾响（0.25）按淡化权重平滑衰减，同时新核
ma48 的响应平滑爬升，无任何突变。

## 测试结果（python3.11 -m unittest test_kernel_switch）

```
Ran 13 tests in 0.019s
OK
```

覆盖：切换点前后脉冲响应的时域参考比对、短核→长核（64 阶）无越界、
淡化中再次切换的合成参考比对、取消切换回到源核、等增益常量信号无台阶、
块大小 1/3/7/64/257/1000 比特级一致、淡化中快照恢复比特级一致、
每 3 样本连续切换下有界性、元数据权重和为 1 且时间精确、非法切换拒绝、
零延迟。
