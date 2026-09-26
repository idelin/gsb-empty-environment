# recurrence — 墙钟重复规则引擎（Python 3.11，仅标准库）

按查询窗惰性展开的重复规则库：日 / 周（指定星期）/ 月（指定日期）规则，
多规则交并差，排除日期（exdate）与额外日期（rdate）合并，k-th 事件定位，
合成偏移转换表（跳时 / 重复时段按显式策略处理），以及绑定规则指纹与
表版本的分页游标。测试基于 `unittest`。

## 时间模型（三种概念严格区分）

- `WallClock`：朴素本地日期时间（墙钟），规则定义在墙钟时间上；拒绝
  带时区或亚秒值，保证相等、排序、去重是全序且精确的。
- `Instant`：UTC 时间轴上的点，整数 Unix 秒。只能由 `WallClock` 经
  `OffsetTable` 转换得到。
- `Duration`：精确秒数时长。`Instant + Duration` 永远合法；月历规则
  **不**按固定秒数推进，而是按墙钟日历月推进（见测试
  `test_monthly_rule_does_not_advance_by_fixed_seconds`）。

## 语义与策略

- 所有查询窗为半开区间 `[start, end)`，输出按墙钟时间升序去重。
- 规则惰性展开：只枚举窗口内的发生，绝不无界枚举。
- 计数 / k-th 定位走算术跳跃而非逐日扫描：日规则 O(1)、周规则 O(1)
  （仅扫描边界周）、月规则 O(月数)（绝不逐日）。每次 `count_between`
  的跳跃统计写入 `rule.last_meta`，测试用它与小窗口逐日暴力参考核验。
- 每月 31 日规则跳过无 31 日的月份，不回退到其他日期。
- 组合语义：include 规则取并集；候选必须被所有 intersect 规则包含、
  且不被任何 exclude 规则包含（交并差）。
- 冲突优先级：**exdate 胜过同刻 rdate**（排除永远生效，因此排除必然
  影响计数与排名，见 `test_exdate_affects_count_and_rank`）。
- 跳时（gap）策略：`GapPolicy.SHIFT_FORWARD`（墙钟前移 gap 长度后解析）
  或 `GapPolicy.REJECT`（抛 `NonexistentWallTime`）。
- 重复时段（fold）策略：`FoldPolicy.EARLIER` / `FoldPolicy.LATER`。
- 分页游标 `Cursor` 绑定规则集指纹与偏移表版本，跨规则集或跨表版本
  续传会抛 `ValueError`；游标可 base64 编解码往返。

## 文件

- `recurrence/timeprims.py` — `WallClock` / `Instant` / `Duration`
- `recurrence/offsettable.py` — 版本化偏移转换表、gap/fold 策略
- `recurrence/rules.py` — `DailyRule` / `WeeklyRule` / `MonthlyRule`
- `recurrence/ruleset.py` — 交并差、exdate/rdate、游标分页
- `fixtures.py` — 合成偏移转换表夹具（含 1h 与 30min 的 gap/fold）
- `demo.py` — 运行样例
- `test_recurrence.py` — 29 个 unittest 用例

## 运行

```console
$ python3 demo.py          # 运行样例
$ python3 -m unittest test_recurrence -v
```

## 运行样例（节选自 `python3 demo.py`）

```text
== monthly 31st (wall -> UTC instant, delta to previous) ==
2026-01-31T09:00:00  ->  2026-01-31T08:00:00+00:00   (+0s)
2026-03-31T09:00:00  ->  2026-03-31T07:00:00+00:00   (+5094000s)
2026-05-31T09:00:00  ->  2026-05-31T07:00:00+00:00   (+5270400s)
2026-07-31T09:00:00  ->  2026-07-31T07:00:00+00:00   (+5270400s)
2026-08-31T09:00:00  ->  2026-08-31T07:00:00+00:00   (+2678400s)
2026-10-31T09:00:00  ->  2026-10-31T08:00:00+00:00   (+5274000s)
2026-12-31T09:00:00  ->  2026-12-31T08:00:00+00:00   (+5270400s)

== daily ∩ Mon/Wed/Fri − 12th (+extra 1/3, exdate beats rdate on 1/12) ==
2026-01-03T09:00:00
2026-01-05T09:00:00
2026-01-07T09:00:00
2026-01-09T09:00:00
2026-01-14T09:00:00
2026-01-16T09:00:00
2026-01-19T09:00:00

== daily 02:30 across spring-forward gap (shift-forward policy) ==
2026-03-29T02:30:00  ->  2026-03-29T01:30:00+00:00   # 墙钟 02:30 不存在，前移 1h

== daily 02:30 across fall-back fold (EARLIER vs LATER) ==
earlier: 2026-10-25T02:30:00 -> 2026-10-25T00:30:00+00:00
later:   2026-10-25T02:30:00 -> 2026-10-25T01:30:00+00:00

== daily count 2000..4000 (no per-day scan) ==
count = 730485
meta  = {'periods_jumped': 730485, 'periods_scanned': 0}
kth(500000) = 3368-12-14T08:00:00

== exdate shifts rank ==
count Jan-2026 = 30
2nd occurrence (k=2) = 2026-01-04T09:00:00

== paged iteration, limit=10 ==
page 1: 2026-01-01 .. 2026-01-10  (10 items)  next=eyJmcCI6ICJmZjYzNmQyZTBmZWZhZGU4...
...
page 4: 2026-01-31 .. 2026-01-31  (1 items)  (last)
```

## 测试结果

```text
$ python3 -m unittest test_recurrence
.............................
----------------------------------------------------------------------
Ran 29 tests in 0.014s

OK
```

覆盖：日/周/月规则计数与暴力逐日参考一致性、大跨度跳跃统计、每月 31 日
跳月、月历非固定秒推进、k-th 定位（含排除后排名位移）、并/交/差、
exdate 优先于 rdate、半开窗口与去重、gap/fold 各策略（含 30 分钟
转换）、墙钟往返转换、游标分页与单次遍历一致性、游标跨规则集/跨表
版本拒绝、时间原语精度约束。
