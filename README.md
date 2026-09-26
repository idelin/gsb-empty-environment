# xfade — 时间轴交叉淡化拼接引擎

纯标准库实现（Python 3.11），不接触音频设备；样本数组与采样率由本地夹具
`fixtures.py` 提供（`SAMPLE_RATE = 48000`）。

## 模型

- 片段（`Segment`）按绝对样本位置放在全局输出时间轴上，可任意重叠。
- 重叠处在**指定淡化长度**内做交叉淡化，曲线可选线性（`LINEAR`）或
  等功率（`EQUAL_POWER`）。
- 淡化编译为**每条片段一条全局增益包络**后求和，并在每个输出样本上检查
  总增益约束（`max_total_gain`，默认线性 1.0、等功率 √2）；不会对每对相邻
  片段独立淡化而造成重复缩放。淡化区之间可再次交叠（三重交叠等），约束
  检查在编译后的总曲线上进行，超限抛 `GainConstraintError`。
- 拼接（`concatenate`）中交叉区长度只计一次：总长 = Σlength − (n−1)·fade。

## 声明

- **延迟**：`xfade.LATENCY_SAMPLES = 0`（离线渲染，淡化不引入时间偏移）。
- **数值容差**：`xfade.TOLERANCE = 1e-9`。同一组贡献按不同顺序累加
  （随机分块执行计划）在此容差内视为等价；同序重渲染则位级一致。

## 淡化权重（k 为淡化区内 0 基样本序号，n 为淡化长度）

| 曲线   | 淡入              | 淡出                |
|--------|-------------------|---------------------|
| 线性   | k / n             | 1 − k / n           |
| 等功率 | sin(πk / 2n)      | cos(πk / 2n)        |

## 冲突与边界规则

- 只有实际与别的片段重叠的一侧才施加淡化。
- 短片段（`ShortPolicy`）：
  - `SHORTEN`（默认）：每侧淡化先取 `min(fade_len, length)`；若两侧放不进，
    按比例缩短，`fade_in = floor(fade_in·length/total)`，
    `fade_out = length − fade_in`（淡出取余数）。
  - `REJECT`：所需淡化超过片段长度即抛 `ShortSegmentError`。
- 空片段（length = 0）被完全忽略，不改变输出、映射或他人包络。
- 源区间在读取前校验，越界抛 `SourceBoundsError`，不会越界读。
- 反向片段反向读取源区间 `[start, start+length)`，包络按输出时间施加。

## 接口

- `render(segments, sources, fade_len, curve, ...)` → `(output, Compiled)`；
  `Compiled.maps` 提供源样本 ↔ 输出样本双向映射
  （`source_to_output` / `output_to_source` / `output_to_sources`）。
- `EditSession`：增量编辑（add/remove/replace/move），只重渲染受影响区间
  （变更片段新旧跨度向两侧各扩 fade_len），返回 `EditResult`（受影响输出
  区间 + 修订的源区间）；`verify()` 与全量重渲染位级一致。
- `build_plan` / `execute_plan`：预生成定长块混合计划，可随机分块乱序执行，
  结果与直接渲染在容差内等价。

## 运行

```
python3.11 demo.py          # 运行样例
python3.11 -m unittest test_xfade -v
```

### 样例输出

```
sample rate      : 48000 Hz
declared latency : 0 samples
declared tolerance: 1e-09
output length    : 4988 samples (= 5500 - 2 * 256, crossfade counted once)
peak total gain  : 1.414214 (limit sqrt(2) = 1.414214)
peak |sample|    : 0.698640
mapping example  : 'echo' source 1200 -> output 4787; output 4787 -> source 1200
plan ops         : 44 blocks of <=128 samples
random-chunk worst diff over 8 seeds: 0.000e+00 (< tolerance: True)
```

### 测试结果

```
Ran 15 tests in 0.012s
OK
```

覆盖：线性/等功率逐样本参考、交叉区只计一次、短片段缩短与拒绝、三重交叠
（淡化区互相交叠）逐样本参考、总增益约束强制、反向片段逐样本与映射、
空片段无影响、越界拒绝、映射往返、随机增量编辑 vs 全量渲染位级一致、
计划随机分块执行等价、延迟/容差声明、空时间轴。
