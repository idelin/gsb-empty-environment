# priority_timeline

把带优先级的区间值叠成一条不重叠的时间轴：最高优先级生效，同级冲突
显式保留，支持局部更新与来源跟踪。纯标准库（Python 3.11），测试用
`unittest`。

## 时间模型（`priority_timeline/time_model.py`）

三种概念严格区分，跨类型比较/运算直接抛 `TypeError`：

- `TimePoint`：合成时间轴上的**时间点**，偏移量是 `Fraction`（精确，
  拒绝 `float`）。
- `WallClock`：**墙钟时间**，带时区的 `datetime`（无时区抛 `ValueError`）。
- `Duration`：**持续时长**，秒为单位的 `Fraction`，`1/3 + 1/3 + 1/3 == 1`
  精确成立。
- `OffsetTable`：**合成偏移转换表**，锚点 `(偏移, WallClock)` 分段线性
  映射，锚点精确、段内 `Fraction` 插值、表外沿最近段斜率外延；提供
  `wallclock_at / timepoint_at / duration_between / advance` 双向换算。

## 叠加时间轴（`priority_timeline/core.py`）

- `Timeline.add(start, end, value, priority=, source=)`：插入半开区间
  `[start, end)` 条目，返回条目 id。
- 解析后的 `Segment` 不重叠、按起点升序；每个位置只有最高优先级生效。
- **同级冲突显式保留**：并列最高优先级的候选全部留在冲突段的
  `entries/values/sources` 中，`seg.value` 抛 `ConflictError`，
  `tl.conflicts()` 列出全部冲突段。
- **局部更新**：`add / remove / withdraw_source` 只重算与改动区间重叠
  的窗口，窗口外的分段对象原样保留；撤回高优先级条目后底层自动恢复。
- **来源跟踪**：每个 `Entry` 带来源标识，分段可回溯到贡献它的原始
  条目（证据）；相邻同值同来源的段会合并（双方条目都留痕），相邻同值
  **不同来源**的段不合并，证据不丢。

## 运行

```bash
python3.11 examples/demo.py          # 运行样例
python3.11 -m unittest discover -s tests -v   # 测试
```

## 测试结果

```
.................................
----------------------------------------------------------------------
Ran 33 tests in 0.001s

OK
```

覆盖：高优先 `[3,7)` 仅覆盖低优先 `[0,10)` 中段、撤回高优先恢复底层、
同级冲突显式保留与消解、相邻同值不同来源不合并、局部更新不影响窗口外
分段、Fraction 精确边界、转换表插值/外延/往返换算、三类时间概念的
类型隔离。
