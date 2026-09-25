# timestack — 优先级区间时间轴

将带优先级的区间值叠成一条**不重叠**的时间轴：最高优先级生效，同级冲突显式保留，
支持局部增量更新与来源（provenance）跟踪。纯 Python 3.11 标准库实现，测试基于 `unittest`。

## 核心语义

- 源区间为半开区间 `[start, end)`，携带 `value`、`priority`、`source`。
- 每个时间点上，覆盖该点的最高优先级区间生效；低优先级只被覆盖相交部分
  （高优先 `[3,7)` 压过低优先 `[0,10)`，只影响中段）。
- 同级多源**取值一致**：合并为一段，来源集合取并集（证据不丢）。
- 同级多源**取值冲突**：生成 `Conflict` 段，显式列出每个 `(value, sources)` 备选。
- 相邻解析段仅在 **值、冲突状态、来源集合完全一致** 时才合并——
  同值不同来源的相邻段绝不合并，证据不丢。
- 撤回（`remove(token)`）只使该区间范围失效并局部重解析，底层自动恢复。

## 时间模型（`timestack/timeaxis.py`）

三种概念严格区分，非法混用直接 `TypeError`：

| 类型 | 含义 | 合法运算 |
|---|---|---|
| `Duration` | 持续时长（精确有理数秒） | `±Duration`、`×/÷ 数`、`÷ Duration → Fraction` |
| `Instant` | 时间轴上的**时间点** | `±Duration → Instant`，`−Instant → Duration` |
| `WallClock` | 墙钟读数（**不是**时间点） | `±Duration → WallClock`，`−WallClock → Duration` |

- 全部算术基于 `fractions.Fraction`，精确无漂移（`10 × 1/10 == 1` 严格成立）。
- `OffsetTable`：**合成偏移转换表**，条目为 `(wall_start, offset)`，
  `wall → instant = wall + offset`。偏移上调在时刻空间留下缺口
  （`to_wall` 落入缺口抛 `GapError`）；偏移下调会造成歧义，建表时直接拒绝。
  `OffsetTable.synthetic(base_offset, steps)` 便于构造合成表。

## 快速开始

```python
from timestack import Timeline

tl = Timeline()
tl.add("sensor-low", 0, 10, "L", priority=0)
token = tl.add("manual-high", 3, 7, "H", priority=10)

for p in tl.pieces():
    print(p.start.offset, p.end.offset, p.value, sorted(p.sources))
# 0 3 L ['sensor-low']
# 3 7 H ['manual-high']
# 7 10 L ['sensor-low']

tl.remove(token)          # 撤回高优先，底层恢复
tl.value_at(5)            # ('L', frozenset({'sensor-low'}))
```

墙钟输入需配偏移表：

```python
from timestack import OffsetTable, Timeline, WallClock

table = OffsetTable.synthetic(base_offset=2, steps=[(100, 5)])
tl = Timeline(offset_table=table)
tl.add("w", WallClock(10), WallClock(20), "W", priority=0)
```

## 局部更新

`Timeline` 内部维护解析缓存与脏区间：每次增删只把受影响区间标记为脏，
读取时仅重解析该窗口并与缓存拼接（跨边界只在来源集合一致时合并）。
`tests/test_timeline.py::TestLocalUpdates::test_incremental_matches_rebuild`
在每一步增删后都与全量重建结果对比，保证增量与重建等价。

## 运行

```bash
python3.11 -m unittest discover -s tests -v   # 32 个测试
python3.11 examples/demo.py                    # 场景演示
```

## 布局

```
timestack/
  timeaxis.py    # Duration / Instant / WallClock / OffsetTable（精确时间）
  timeline.py    # Timeline / Piece / Conflict（优先级叠加 + 局部更新 + 来源跟踪）
tests/           # unittest 测试套件
examples/demo.py # 端到端演示
```
