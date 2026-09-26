# xsplice

纯 Python 3.11 标准库实现的样本数组交叉淡化拼接库。不接音频设备；
样本数组与采样率由本地夹具（`xsplice/fixtures.py`）确定性地生成。

## 特性

- **指定长度的交叉淡化**：淡化长度以样本数计，支持 `linear`（线性）
  与 `equal_power`（等功率，sin/cos）两种曲线。
- **全局增益曲线**：每个片段拥有独立的增益包络（淡入斜坡、稳态 1.0、
  淡出斜坡），编译器把所有包络求和成一条全局增益曲线，并在多个片段
  重叠的每个样本上检查总增益约束（默认功率和 ≤ 1.0 + 容差，可选幅度
  和上限）。不做"每对相邻片段独立淡化再重复缩放"。淡化区之间再次
  交叠（含三重交叠）也在同一条全局曲线上逐样本验证。
- **短片段规则明确**：`fade_in + fade_out <= duration` 才合法；不足时
  `on_short="shorten"` 按比例（保比值、向下取整）缩短两个淡化使其
  恰好容纳，`on_short="reject"` 抛 `ShortSegmentError`。规则是全的：
  有效淡化永不越出片段，绝不越界读源。
- **交叉区只计一次**：输出总长 = 各片段时长之和减去重叠；重叠区在
  时间轴上只出现一次（在映射中同时属于两个片段的区间）。
- **空片段不改变结果**：`src_start == src_end` 的片段在编译期被丢弃。
- **反向片段**：`src_end < src_start` 即反向播放，边界检查对称。
- **源到输出映射**：`plan.intervals`、`plan.output_to_source(t)`、
  `plan.source_to_output(seg_id, src_index)`。
- **增量修订**：`Session.upsert/remove` 返回 `Revision`，给出需要
  重渲染的输出区间与受影响的来源区间；编译失败时会话保持原状。
- **分块执行等价**：渲染是计划的纯函数（零前瞻），`render_chunked`
  以任意随机块大小执行与整体渲染逐位一致。

## 声明

- **延迟**：`DECLARED_LATENCY = 0` 样本（输出样本 t 只依赖映射位置的
  源样本，无前瞻）。
- **数值容差**：增益约束检查默认 `DEFAULT_TOLERANCE = 1e-9`；测试中的
  逐样本比较使用 `1e-12`。

## 淡化约定

长度为 N 的淡化覆盖局部样本 0..N-1，归一化位置 `x = i / N`：淡入从
增益 0.0 开始，在淡化区之后第一个样本达到 1.0（淡出对称）。因此配对
的线性交叉淡化每样本幅度和恒为 1，等功率交叉淡化每样本功率和恒为 1。

## 用法

```python
from xsplice import EQUAL_POWER, Segment, Session, fixtures, render_chunked

sources = fixtures.make_sources()          # 本地夹具：样本数组 + 采样率
session = Session(sources, fixtures.SAMPLE_RATE)
session.upsert(Segment("a", "verse", 0, 1200, 0,
                       fade_out=240, curve=EQUAL_POWER))
session.upsert(Segment("b", "chorus", 0, 1200, 960,
                       fade_in=240, curve=EQUAL_POWER))
plan = session.plan                        # 预生成计划
out = render_chunked(plan, [97, 3, 512])   # 任意分块，与整体渲染等价
print(plan.intervals)                      # 源 -> 输出映射
rev = session.upsert(Segment("b", "chorus", 100, 1300, 960,
                             fade_in=240, curve=EQUAL_POWER))
print(rev.changed_output, rev.changed_source)  # 增量修订区间
```

## 运行

```sh
python3.11 -m unittest discover -s tests -v   # 55 个测试
python3.11 examples/demo.py                   # 运行样例
```

## 目录

- `xsplice/curves.py` — 线性 / 等功率曲线
- `xsplice/segment.py` — 片段模型、校验、短片段规则
- `xsplice/plan.py` — 编译：全局增益曲线、总增益约束、映射
- `xsplice/render.py` — 整体 / 区间 / 分块渲染
- `xsplice/session.py` — 增量编辑会话与 Revision
- `xsplice/fixtures.py` — 确定性本地夹具（样本数组 + 采样率）
- `tests/` — unittest 套件（含独立逐样本参考实现 `tests/reference.py`）
- `examples/demo.py` — 端到端样例
