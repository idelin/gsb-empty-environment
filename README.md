# 有效时长计算(effective_time)

基于**每日开放段**与**例外关闭段**计算两时点间的有效时长;跨偏移转换一律按**真实持续时间**计;支持从起点加有效时长**反求终点**。纯 Python 3.11 标准库实现,测试基于 `unittest`。

## 三种时间概念(严格区分,混用抛 `TypeError`)

| 类型 | 含义 | 表示 |
| --- | --- | --- |
| `WallTime` | 墙钟时间:本地钟表显示,不含偏移 | naive `datetime` |
| `Instant` | 时间点:绝对时间轴上的位置,与偏移无关 | 自纪元起的微秒整数 |
| `Duration` | 持续时长:两时点间的真实经过时间 | 微秒整数 |

`Instant + Duration -> Instant`,`Instant - Instant -> Duration`;`Instant + Instant`、`Duration + Instant` 等无意义运算直接报错。

## 合成偏移转换表

`OffsetTable(base_offset, transitions)` 由基准偏移与若干 `OffsetTransition` 构成。每条转换记录跳变前的墙钟时刻与前后偏移,库据此在 `WallTime` 与 `Instant` 间双向换算:

- 前拨产生的**不存在墙钟时间**一律顺延到空洞结束;
- 回拨产生的**重叠墙钟时间**由 `ambiguous="first"/"last"` 选择。

## 日历与计算

`EffectiveCalendar(table, windows, closed)`:

- `windows`:每日开放段 `DailyWindow(start, end)`(墙钟,当日 `start < end`);
- `closed`:例外关闭段 `ClosedInterval(start, end)`(墙钟半开区间),先合并再扣除,**重叠部分只扣一次**;
- `effective_duration(start, end) -> Duration`:两 `Instant` 间的有效时长;
- `add(start, duration) -> Instant`:反求终点,沿开放段累积真实时长;**落点位于关闭段(含开放段结束边界)时跳到下一开放段起点**。

关键语义:开放 01:00–04:00 遇前拨 1 小时,墙钟 3 小时,实际有效时长 **2 小时**。

## 运行

```bash
python3.11 demo.py        # 运行样例
python3.11 -m unittest -v # 运行测试(20 个用例)
```

## 样例输出(节选)

```
== 场景 1:前拨日开放 01:00-04:00,实际只有 2 小时 ==
  墙钟跨度 3 小时,有效时长 = 2.0 小时
== 场景 3:反求,从起点加同样的有效时长 ==
  反求终点: 墙钟 2024-03-12 16:00:00
  与正算终点一致: True
== 场景 4:反求落在关闭段,跳到下一开放段 ==
  09:00 + 3h: 墙钟 2024-03-08 13:00:00
```

测试结果:`Ran 20 tests ... OK`(类型区分、偏移换算、正算、反求、重叠关闭段、边界跳转、往返一致性)。
