# session-windows

增量会话窗口库（Python 3.11+）。面向遥测事件流：乱序、迟到、重复投递、
撤回都是常态，本库在维护当前窗口的同时，为每条输入输出
**retract / upsert / close 变更流**，让下游消费者无需全量重算即可
无歧义地更新已有结果。

## 概念

- **事件** `Event(source, seq, event_ms, value)`：`(source, seq)` 是事件的
  稳定标识，撤回操作凭此寻址（不需要知道事件时间）。
- **撤回** `Retract(source, seq)`：指向已接受事件的标识。
- **会话**：同一 `source` 内，相邻事件间隔 `<= gap_ms` 即属于同一会话。
- **窗口 ID**：`"<source>@<start_ms>"`，`start_ms` 是窗口内最小事件时间。
  ID 是窗口内容的纯函数，因此**最终窗口集合（含 ID 与成员）与到达顺序
  无关**——三种（任意）到达顺序收敛到同一结果。

## 窗口 ID 延续规则（合并 / 拆分）

- **插入合并** k 个窗口：合并后的窗口以其新起点对应的 ID 做 `UPSERT`；
  所有不等于新 ID 的被合并窗口 ID 各发一条 `RETRACT`。常见情形（新事件
  不比所有被合并窗口更早）下，最早窗口的 ID 自然延续；若迟到事件落在
  所有被合并窗口之前，窗口起点前移，旧 ID 被 retract、以新起点 ID 出现。
- **撤回拆分**为 n 段：每段各发一条 `UPSERT`（最早一段通常保住原 ID）；
  仅当没有任何一段保住原 ID（被删的是窗口最早事件，或窗口被删空，n=0）
  时才对旧 ID 发 `RETRACT`。
- 同一修订内 `RETRACT` 永远排在 `UPSERT` 之前。

## 处置结果（Disposition）

每条输入都有明确的处置结果：

| Disposition | 含义 |
|---|---|
| `ACCEPTED` | 事件被接受，`changes` 描述窗口变更 |
| `DUPLICATE` | `(source, seq)` 已存在，重复投递被忽略（先写胜出） |
| `DROPPED_LATE` | `event_ms < watermark - allowed_lateness`，超出允许迟到范围，被丢弃 |
| `RETRACTED` | 撤回生效 |
| `RETRACT_UNKNOWN` | 撤回指向不存在的标识（从未见过 / 已撤回 / 已被水位线回收），空操作 |
| `WATERMARK` | 水位线推进（不前进时是合法空操作） |

## 水位线与状态回收

- 水位线按 source 独立推进：`advance_watermark(source, watermark_ms)`。
- 窗口满足 `end + gap_ms < watermark - allowed_lateness_ms` 时**闭合**：
  发出最后一条 `CLOSE` 变更（含最终快照，`closed=True`），随后其会话
  状态与身份索引中的成员事件被回收。
- 回收是惰性的：只在 `AdvanceWatermark` 操作时发生（即使水位未实际
  前进也会扫描一次，因为撤回可能已缩短窗口末端）。
- 回收后：迟到的补数得到 `DROPPED_LATE`；针对已回收事件的撤回得到
  `RETRACT_UNKNOWN`。两者都是显式、可区分的处置结果。

## API

```python
from session_windows import (
    SessionWindows, Event, Retract, AdvanceWatermark,
)

sw = SessionWindows(gap_ms=5, allowed_lateness_ms=10)

# 逐条接口：每次调用返回一个 Revision（含 disposition、changes、stats）
rev = sw.feed(Event("s", 1, 0))
rev = sw.feed(Event("s", 2, 9))          # 两个窗口: s@0, s@9
rev = sw.feed(Event("s", 3, 4))          # 迟到桥接 -> RETRACT s@9 + UPSERT s@0
rev = sw.retract("s", 3)                 # 撤回桥接 -> UPSERT s@0 + UPSERT s@9
rev = sw.advance_watermark("s", 100)     # -> CLOSE s@0, CLOSE s@9

# 批量接口：按序应用，返回每个 op 的 Revision 以及拼接后的变更流
batch = sw.apply([Event("s", 4, 200), Retract("s", 4)])

# 当前（未闭合）窗口快照
windows = sw.snapshot()
```

下游消费者只需维护一张 `id -> Window` 的表：`RETRACT` 删除、`UPSERT`
覆盖、`CLOSE` 落终态。测试 `test_downstream_consumer_replays_changelog`
验证了该消费者与引擎快照逐步一致。

## 索引与每次修订的更新量

每个 source 只维护两个索引，任何修订都不扫描全部保留历史：

| 索引 | 结构 | 用途 |
|---|---|---|
| `events` | `dict[seq -> Event]` | 去重、撤回寻址，O(1) |
| `sessions` | 按起点排序、互不重叠的会话列表 | 二分定位，O(log s) |

- **插入**：二分找到与 `[t-gap, t+gap]` 相交的连续会话段（k 个），只归并
  这 k 个窗口的成员（`heapq.merge`），代价 O(log s + k + m)，m 为被合并
  窗口的成员总数；`sessions` 列表做一次局部 splice。
- **撤回**：`events` 字典 O(1) 命中事件时间，二分定位所在窗口，只对该
  窗口的成员重新分段，代价 O(log s + m)，m 为单个窗口大小。
- **水位线**：会话互不相交且按起点排序，末端单调递增，前缀扫描即得
  可回收集合，代价 O(回收个数)。

每个 `Revision` 携带 `IndexStats`（`sessions_merged`、`segments_produced`、
`members_touched`、`sessions_reclaimed`、`events_reclaimed`），调用方
可以据此核验单次修订的工作量与上述界一致。

## 对照实现与测试

`session_windows.reference.ReferenceSessionWindows` 是独立的小规模对照
实现：每条操作后把全部存活事件重新排序、从头重算所有窗口，再 diff 出
变更流。`tests/test_differential.py` 用随机操作序列（插入、重复投递、
撤回存活/未知标识、水位线推进）对两个实现做逐操作对拍，要求 disposition、
变更流、快照三者完全一致；另有一个只用引擎变更流驱动下游表、与参照
实现逐步比对的测试。

```bash
python3 -m pytest        # 运行全部测试
```

覆盖：窗口合并/拆分与 ID 延续、三种及全排列到达顺序收敛、窗口闭合与
回收、允许迟到边界、重复投递、撤回不存在/已回收事件、逐条与批量接口
等价、下游变更流回放。
