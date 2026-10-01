# calpart — 本地历法时间窗分区库

把一个 UTC 长时间窗切成**本地日 / 周 / 月**边界，输出每段对应的 UTC 区间与真实
时长；支持自定义周起始、合成偏移转换表、局部偏移表修订、多历法共同细分与惰性
分页。全部端点用 `Fraction` 精确表示，只用 Python 3.11 标准库。

## 核心概念

- **三种时间类型严格区分**（`calpart/exact.py`）：
  - `Instant`：绝对 UTC 时间点；
  - `Wall`：本地墙钟读数（不携带偏移）；
  - `Duration`：精确经过时长。
  非法混合运算（如 `Instant - Wall`）直接抛 `TypeError`；浮点数被拒绝，端点始终
  是精确数（`Fraction` 秒）。
- **偏移表**（`calpart/offsets.py` 的 `OffsetTable`）：分段常数 UTC 偏移，支持
  合成转换表与**局部修订** `revise(span_start, span_end, new_transitions)`——只
  替换落在修订区间内的转换点，区间外不受影响。
- **历法网格**（`calpart/grids.py`）：`DayGrid`、`WeekGrid(week_start)`（0=周一，
  可自定义周起始）、`MonthGrid`（格里高利历，12 月自然跨入次年 1 月，不漏段）。
  历法换算用纯整数算法（Hinnant 算法），不依赖 `datetime` 的年份上限。
- **分区**（`calpart/partition.py`）：`iter_segments` 惰性地产出 `Segment`，每段
  带历法标签、精确 UTC 端点与真实时长。偏移回拨日真实时长为 25 小时（90000 s），
  春拨日为 23 小时。**不假设每天只有一个连续原像**：偏移表让本地时间重复时，同一
  个本地日会产生多个互不相交的 UTC 段（见样例 4）。
- **共同细分**（`calpart/refine.py` 的 `common_refinement`）：多个本地历法分区交
  叠时，按所有历法的切点构建原子细分；每段报告各历法标签，并给出每个标签的
  **全部 UTC 原像**（`refinement.preimage_of(calendar, label)`，可能是不相交的多段）。
- **惰性分页**：`SegmentPager(iter_segments(...), page_size)` 按需拉取页。
- **覆盖守恒核验**（`calpart/verify.py`）：
  - `verify_piecewise_conservation`：参考实现，在每个偏移转换点切割，逐段核验
    墙钟长度 == UTC 长度，且各段精确铺满窗口；
  - `verify_segments_cover`：核验段并集正好覆盖原窗、无重叠、无缺口；
  - `jump_scan`：极长窗口按常数偏移拉伸做**算术跳跃**，把跳跃工作量记录在
    `WorkStats`（`bulk_jumps` / `periods_jumped`），而不是逐秒或逐段扫描——
    10 万年窗口也只扫描 O(转换点数) 次。

## 运行

```bash
python3.11 demo.py                 # 运行样例
python3.11 -m unittest discover -s tests -v   # 运行测试（52 个）
```

## 样例输出（节选自 `demo.py`）

```
=== 1. Local days across the fall-back transition (25h day) ===
  2024-10-26  UTC [2024-10-26T00:00:00Z .. 2024-10-26T22:00:00Z)  true duration 22h
  2024-10-27  UTC [2024-10-26T22:00:00Z .. 2024-10-27T23:00:00Z)  true duration 25h
  2024-10-28  UTC [2024-10-27T23:00:00Z .. 2024-10-28T23:00:00Z)  true duration 24h
  2024-10-29  UTC [2024-10-28T23:00:00Z .. 2024-10-29T00:00:00Z)  true duration 1h
  coverage: union of segments == window, disjoint, exact

=== 4. One local day with two disjoint UTC preimages (30h offset jump) ===
  2024-01-02  UTC [2024-01-02T00:00:00Z .. 2024-01-02T12:00:00Z)  12h
  2024-01-02  UTC [2024-01-03T06:00:00Z .. 2024-01-04T00:00:00Z)  18h
  label 2024-01-02 owns 2 disjoint UTC pieces, total 30h

=== 5. Common refinement of overlapping calendars ===
  [2024-10-26T12:00:00Z .. 2024-10-26T18:30:00Z)  23400s  berlin=2024-10-26, kolkata=2024-10-26, iso-week=week[Mon]@2024-10-21
  [2024-10-26T18:30:00Z .. 2024-10-26T22:00:00Z)  12600s  berlin=2024-10-26, kolkata=2024-10-27, iso-week=week[Mon]@2024-10-21
  ...
  preimages of berlin/2024-10-27: [2024-10-26T22:00:00Z .. 2024-10-27T23:00:00Z)  (total 25h)

=== 9. Enormous window verified by jumping (no per-second scan) ===
  window length: 3155695200000s (~100 000 years)
  work recorded: WorkStats(pieces_checked=5, transitions_scanned=4, bulk_jumps=5, periods_jumped=36524255)
```

## 测试结果

```
$ python3.11 -m unittest discover -s tests
....................................................
----------------------------------------------------------------------
Ran 52 tests in 0.885s

OK
```

覆盖：精确类型与分数端点、历法换算（与 `datetime` 对照 3000+ 采样）、偏移表查
询/多原像/局部修订、日/周/月网格（含自定义周起始、闰年、跨年）、25h/23h 日、
同一本地日的不相交多原像、月末跨年不漏段、惰性分页、多历法共同细分与标签原像、
逐转换点覆盖守恒、10 万年窗口跳跃核验。

## 目录结构

```
calpart/
  exact.py      # Instant / Wall / Duration，精确 Fraction 端点，历法换算
  offsets.py    # OffsetTable：分段常数偏移、局部修订、本地区间原像
  grids.py      # DayGrid / WeekGrid(自定义周起始) / MonthGrid
  partition.py  # iter_segments（惰性）、partition、SegmentPager（分页）
  refine.py     # common_refinement：多历法共同细分 + 标签的全部 UTC 原像
  verify.py     # 逐转换点守恒核验、覆盖核验、跳跃扫描（WorkStats）
demo.py         # 10 个可运行样例
tests/          # 52 个 unittest 用例
```
