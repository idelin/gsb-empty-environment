# calpart — 按本地日历边界精确切分 UTC 时间窗

纯 Python 3.11 标准库实现（仅 `unittest` 做测试）。把长时间窗按本地
日 / 周 / 月边界切分，输出每段的 UTC 区间与真实时长；多个本地历法分区
交叠时构建共同细分（common refinement），每段携带各历法标签与**全部**
UTC 原像。

## 概念区分（不可混用）

- `WallTime`：墙钟时间（naive 本地读数：日期 + 当天秒数），不在物理时间轴上。
- `Instant`：时间点（UTC 时间轴上的点，距 1970 纪元的精确秒数）。
- `Duration`：持续时长（无位置的精确时间长度）。

所有端点与时长均为 `fractions.Fraction` 精确数，区间为半开 `[start, end)`。

## 能力一览

- `OffsetTable`：合成偏移转换表（分段常数 offset 函数）。
  - `instant_to_wall` / `wall_to_instants`：双向转换；墙钟时间的 UTC 原像
    可为 0 个（被跳过）、1 个或多个（重复出现），**不假设每天只有一个连续原像**。
  - `revise(start, end, new)`：局部修订转换表，范围外转换结果逐点不变。
  - `iter_transition_pages(start, end, page_size)`：惰性分页（生成器）。
- 日历分区：`DayCalendar`、`WeekCalendar(week_start)`（自定义周起始，
  0=周一 … 6=周日）、`MonthCalendar`；边界枚举按历法跳跃，不逐秒扫描。
- `partition_window(table, window, calendars, work)`：共同细分。
  切割点 = 窗口端点 ∪ 窗内全部偏移转换点 ∪ 每个历法边界的全部 UTC 原像；
  标签相同的相邻切片合并为一段，同标签的离散 UTC 区间归入同一段。
- 核验：`verify_coverage`（段并集恰好覆盖原窗、无重叠、测度守恒）与
  `reference_verify`（参考实现：逐转换点重新切割后逐片核对归属唯一）。
- `WorkCounter`：记录跳跃工作量（boundary jumps / transition visits /
  wall lookups），极长窗口（如 5000 年）只记录跳跃次数而非逐秒扫描。

## 目录结构

```
calpart/
  exact.py      # WallTime / Instant / Duration / Interval（Fraction 精确数）
  offsets.py    # OffsetTable：转换、全部原像、局部修订、惰性分页
  calendars.py  # 日 / 周（自定义起始）/ 月分区与跳跃式边界枚举
  partition.py  # 共同细分、Segment、覆盖守恒核验、WorkCounter
tests/          # unittest 测试（合成偏移转换表）
examples/demo.py
```

## 运行

```bash
python3.11 examples/demo.py      # 运行样例
python3.11 -m unittest discover  # 运行测试（仓库根目录）
```

## 样例输出（节选）

```
1) Fall-back window: day x week(start=Sunday) x month common refinement
synthetic table: base +02:00, transition 2023-10-29T01:00:00Z -> +01:00
window: [2023-10-27T00:00:00Z, 2023-11-02T00:00:00Z)
  segment 3: day=2023-10-29  week(start=6)=2023-10-29  month=2023-10
    UTC [2023-10-28T22:00:00Z, 2023-10-29T23:00:00Z)  len 25:00:00 = 90000 s
coverage ok: True; total 144:00:00 = 518400 s
reference verify (cut at every transition point): True

2) Offset jumps back 30h: one local date owns two disjoint UTC preimages
  segment 2: day=2020-06-14
    UTC [2020-06-14T00:00:00Z, 2020-06-15T00:00:00Z)  len 24:00:00 = 86400 s
    UTC [2020-06-15T06:00:00Z, 2020-06-16T06:00:00Z)  len 24:00:00 = 86400 s
    total duration: 48:00:00 = 172800 s
coverage ok: True; total 96:00:00 = 345600 s

3) Extreme window: 5000 years by month, jump-based work accounting
window length: 157784803200 s (~5000 years)
segments (months): 60000
boundary jumps: 60001; transition visits: 0; wall lookups: 60001
jump work / window seconds: 7.61e-07 (no per-second scanning)
coverage ok: True; total 157784803200 s
```

要点：偏移回拨日 2023-10-29 真实时长 25 小时；偏移回跳 30h 时本地日期
2020-06-14 拥有两段互不相交的 UTC 原像（共 48 小时）；5000 年窗口仅
约 6 万次边界跳跃即完成切分，工作量与窗口秒数之比约 7.6e-07。

## 测试结果

```
$ python3.11 -m unittest discover
Ran 35 tests in ~10s
OK
```

覆盖：精确原语（分数端点、区间运算）、偏移表（二义/空洞/重复原像、
局部修订范围外不变、惰性分页）、日历（自定义周起始、月末跨年）、
分区（25 小时回拨日、被跳过的日期、双原像日期、跨年不漏段、多历法
共同细分、分数端点窗口、5000 年跳跃工作量、覆盖守恒与参考核验）。
