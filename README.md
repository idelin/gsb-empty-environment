# dyncurve — 动态曲线编译与多通道处理器

纯 Python 3.11 标准库实现(仅 `math`/`bisect`/`dataclasses`/`random`/`unittest`),
不接音频设备;样本数组与采样率由本地夹具 `fixtures.py` 提供(确定性、可复现)。

## 架构:静态曲线与动态包络分离

```
fixtures.py            本地夹具:样本数组 + SAMPLE_RATE = 48000
dyncurve/curve.py      静态曲线编译(dB 域,纯函数,无状态)
dyncurve/envelope.py   包络检测(Peak/RMS)与增益平滑(一阶 IIR,dB 域)
dyncurve/processor.py  多通道处理器:检测 -> sidechain 合并 -> 静态曲线 -> 增益平滑
examples/demo.py       可运行样例:python3.11 examples/demo.py
tests/                 unittest:python3.11 -m unittest discover -s tests -v
```

包络检测与增益平滑**不**依赖静态曲线,三者可独立替换与测试
(`tests/test_envelope.py::test_curve_is_not_part_of_envelope`)。

## 曲线编译

- 段语义:区间斜率由本区段决定——压缩段左缘 `1/ratio`,扩展段右缘 `ratio`,
  否则 1;最低阈值处锚定零增益。
- 硬拐点 `knee_db=0`;软拐点为中心二次曲线,两端**值与斜率均连续**
  (编译期数值校验,容差见下)。
- 编译期校验:值连续(`value_continuity`)、导数条件(`slope_conditions`)、
  单调性(`monotonicity`);重复阈值、拐点重叠、非法 ratio 直接报错。
- `ratio == 1` 为恒等;`ratio == math.inf` 的压缩段产生零斜率**平台**(限制器)。
- 逆查 `inverse_db(y)` 返回区间 `(lo, hi)`;平台为**不可逆区间**(`lo < hi`),
  由 `plateaus()` 列出;超出输出范围返回 `None`。
- 静音契约:电平钳制在 `DB_FLOOR = -160 dBFS`,增益恒为有限值,绝不产生 inf/nan。

## 多通道 sidechain

`SidechainConfig`:`max` / `mean` / `weighted(weights)`(联动增益,共享一个增益
平滑器)/ `independent`(每通道独立检测与平滑)。

## 规则热更新

`processor.set_curve(new_curve, at_sample, fade_samples)`:

- 交叉过渡**精确**从绝对样本点 `at_sample` 开始(已过则立即开始),
  在 dB 域线性混合旧/新曲线增益,长度 `fade_samples`;
- **总延迟不变**:所有环节均为因果零前瞻滤波,声明延迟恒为
  `LATENCY_SAMPLES = 0`,换曲线不改变它;
- **增益不突跳**:过渡斜坡连续,测试断言任意相邻样本增益步进不超过
  计划斜坡斜率(容差 `hot_swap_gain_db`);
- 多次换曲线排队顺序执行;
- **包络状态策略:保留(preserved),不迁移**。检测器与增益平滑器状态原样
  继续运行,仅静态曲线(及其交叉淡变)更换。由
  `tests/test_hotreload.py::test_envelope_state_is_preserved_not_migrated` 验证。

## 级联 ≠ 合参

压缩器 → 扩展器两级级联(两条独立的包络/平滑链)与把参数合并进单级多段曲线
的处理器输出**不同**(静态增益相同,动态行为不同),见
`tests/test_processor.py::TestCascadeVsMerged`。

## 声明:延迟与数值容差

| 名称 | 值 | 含义 |
|---|---|---|
| `LATENCY_SAMPLES` | `0` | 端到端延迟(样本),热更新不改变 |
| `DB_FLOOR` | `-160 dBFS` | 静音电平钳制,增益恒有限 |
| `curve_continuity_db` | `1e-9` | 分段边界值跳变上限 |
| `curve_slope` | `1e-9` | 软拐点两侧斜率失配上限 |
| `static_gain_db` | `1e-9` | 编译曲线 vs 独立解析参考 |
| `sample_exact` | `0.0` | 随机分块 / 状态恢复逐位一致 |
| `hot_swap_gain_db` | `1e-6` | 实际增益相对理想交叉斜坡的偏差上限 |
| `inverse_db` | `1e-9` | 逆查往返误差 |

## 测试

```
python3.11 -m unittest discover -s tests -v     # 38 项
python3.11 examples/demo.py                     # 可运行样例
```

覆盖:硬/软拐点与多段编译、独立解析参考的分段边界检查、值/导数/单调性、
恒等与静音安全、逆查与平台区间、包络/平滑与曲线分离、四种 sidechain 模式、
随机分块与状态恢复逐位一致、级联≠合参、热更新(起点精确、延迟不变、
增益不突跳、包络状态保留、排队换曲线)。
