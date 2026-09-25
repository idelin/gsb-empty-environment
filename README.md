# 日历重复规则引擎（纯标准库）

一个用 Python 3.11 标准库实现的日历调度内核，支持按日、按周（指定星期）、
按月（指定日期）的重复规则，处理排除/额外日期、多规则交并差、第 k 次事件
定位和窗口分页，并在**合成的 UTC 偏移转换表**上显式处理夏令时“跳时”和
“重复时段”。仅依赖标准库，测试使用 `unittest`。

## 运行

```bash
python3 demo.py                                   # 端到端运行样例
python3 -m unittest discover -s tests -v           # 全部测试
```

已保存的真实输出：`docs/demo_output.txt`、`docs/test_results.txt`（40 个测试通过）。

## 模块

- `recur/time_model.py` — 严格区分三类时间量：
  - `WallTime`：墙钟时间（年月日 + 当天微秒），不含偏移，不能直接与时间点比较；
  - `Instant`：UTC 时间线上的绝对时间点，整数微秒；
  - `Duration`：物理持续时长，整数微秒。墙钟相减得到的是“名义时长”，跨转换点时
    与用 `Instant` 相减得到的真实时长不同（测试有断言）。
- `recur/offsets.py` — 偏移转换表与转换策略。表是显式夹具（不读宿主时区库），
  每行给出某 `Instant` 起生效的 UTC 偏移；据此直接算出每段偏移所映射的本地区间
  `[early, late)`：
  - 0 个区间包含某墙钟值 → 落在**跳时缺口**，策略 `Gap.RAISE / FORWARD / BACKWARD`；
  - 1 个 → 唯一映射；
  - 2 个 → **重复时段**（回拨），策略 `Ambiguous.RAISE / EARLIEST / LATEST / BOTH`。
  表内容经 SHA-1 派生 `version`，内容变化则版本变化。
- `recur/fixtures.py` — 合成的 +08:00/+09:00 夏令时表：3 月第二个周日 02:00 跳时
  （02:00–03:00 不存在），11 月第一个周日 02:00 回拨（01:00–02:00 出现两次）。
- `recur/rules.py` — `DailyRule / WeeklyRule / MonthlyRule`、`Schedule`、`Window`、
  `Event`、`Expansion`。
- `recur/composite.py` — `union / intersection / difference`，在绝对时间线上合并并去重。
- `recur/query.py` — `query_page`（版本化游标分页）与 `nth_event`（第 k 次）。
- `tests/_oracle.py` — 仅供测试的**逐日扫描参考实现**，与跳跃式展开器交叉核验。

## 关键设计如何对应需求

- **日 / 周（指定星期）/ 月（指定日期）**：三种规则；`MonthlyRule(by_monthday=(31,))`
  在没有 31 日的月份不产生事件（2 月等被跳过）。
- **月历不能按固定秒数推进**：月规则以 `year*12+month` 的月份索引跳跃，2 月 28 天、
  3 月 31 天都不影响步长；周规则按 7 天对齐周一起算。
- **惰性展开、不无界枚举**：只展开查询窗（外加 ±2 天安全余量）内的周期，
  生成器按“周期”跳到首个可能周期，`Expansion.jumps` 报告跳跃次数。十年跨度查询
  月规则只用约 121 次月跳跃，而不是扫描 3653 天（见 demo 与
  `test_decade_query_does_not_day_scan`）。
- **大跨度 vs 小窗口核验**：小窗口用逐日扫描参考实现逐天验证事件集合完全一致；
  大跨度断言跳跃次数远小于天数（日 interval=1 仍按天，但周/月严格小得多）。
- **排除与额外合并、同日优先级**：`exdates`（日期集）与 `rdates`（墙钟列表）合并；
  同一墙钟日期上**排除优先于额外**（`test_exclusion_beats_same_day_extra`）。
  排除也影响**计数与排名**（`test_exclusion_changes_count_and_ranking`）。
- **窗口半开、去重**：`Window = [start, end)`；交并差和展开在 `Instant` 去重，
  同一点只出现一次；回拨时段用 `Ambiguous.BOTH` 时两次是不同 `Instant`，都会保留。
- **跳时/重复时段显式策略展开**：缺口可抛错或前移到 03:00 / 后移到前一微秒；
  重复时段可抛错、取最早、取最晚或两者都取。`Event` 同时保留名义墙钟 `wall` 和
  跳时后的实际墙钟 `mapped_wall`。
- **交并差**：在时间点上做并集（去重）、交集、差集。
- **第 k 次定位**：`nth_event(node, k)` 按月块跳跃推进（不逐日），尊重排除/额外与集合运算。
- **游标绑定规则与表版本**：分页游标用 base64(JSON) 记录计划指纹（规则+排除+额外
  +策略的哈希）、偏移表 `version`、窗口边界和“最后一条之后”的位置；任一不符抛
  `StaleCursorError`。
- **时间精确表示**：全程整数微秒，时间线上不出现浮点。
- **Python 3.11 / 标准库 / unittest**：无第三方依赖。

## 最小示例

```python
import datetime as dt
from recur import (Ambiguous, DailyRule, Gap, Instant, MonthlyRule, STD,
                  Schedule, WallTime, Window, make_table, nth_event, query_page)

table = make_table()
sched = Schedule(
    DailyRule(WallTime.from_parts(2024, 1, 1, 8, 30), interval=1),
    table,
    exdates={dt.date(2024, 3, 12)},
    rdates=[WallTime.from_parts(2024, 3, 13, 20, 0)],
    gap=Gap.FORWARD, ambiguous=Ambiguous.EARLIEST,
)
lo = Instant(WallTime.from_parts(2024, 3, 1).epoch_us - STD)
hi = Instant(WallTime.from_parts(2024, 4, 1).epoch_us - STD)
expansion = sched.expand(Window(lo, hi))   # .events, .jumps
page = query_page(sched, Window(lo, hi), limit=10)
third = nth_event(sched, 3)
```
