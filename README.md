# 精确滑动窗口分位数

Python 3.11 标准库实现，无第三方依赖。组件支持事件摄入、按事件身份撤回、水位推进，以及空窗返回空值的精确分位数查询。

## 语义

- 窗口长度为 `window_size`，当前水位为 `w` 时，活动区间是左开右闭的 `(w - window_size, w]`。
- 首次推进水位前，摄入事件只作为未来事件缓存，不会进入活动窗口。
- 事件时间晚于水位时先进入未来堆；水位到达后再激活。
- 事件时间早于或等于左端时拒绝；等于右端时保留。
- 事件身份是 `(source, seq)`。重复身份永远拒绝，撤回或过期后也不会再次接收同一身份。
- 撤回删除一个事件身份，而不是删除一个数值。数值相同的不同事件仍各自占据一个排名位置。
- 活动值排序为 `values` 且窗口非空时，查询结果是 `values[floor(q * (n - 1))]`；`q` 必须在 `[0, 1]`。
- 空窗口的 `quantile()` 返回 `None`。

## 接口

```python
from quantile_window import Event, ExactQuantileWindow, NaiveSortingWindow

window = ExactQuantileWindow(window_size=10)
window.ingest(Event(time=1, source="sensor-a", seq=0, value=1))
window.advance_watermark(5)
window.quantile(0.5)
window.retract("sensor-a", 0)
window.stats()
```

- `ingest(event) -> bool`：新身份返回 `True`，重复身份返回 `False`。
- `retract(source, seq) -> bool`：撤回当前缓存或活动事件返回 `True`，重复撤回返回 `False`。
- `advance_watermark(watermark) -> AdvanceResult`：水位只能单调向前，返回激活、过期、迟到丢弃和惰性堆项计数。
- `quantile(q)`：返回精确分位数；空窗返回 `None`。
- `stats()`：返回活动数、缓存数、Treap 节点数、两个堆长度和身份墓碑数等维护信息。
- `NaiveSortingWindow`：朴素排序 oracle，接口与主实现对齐，用于随机操作序列对照。

## 维护结构

- `_CountTreap`：随机 Treap，按活动值排序。一个 Treap 节点表示一个不同值，节点中的 `count` 保存重复值个数，`size` 保存子树事件总数。因此重复值各自占排名位置，但不会为每个重复值分配树节点。
- `_active_heap`：活动事件的最小时间堆，用于批量找到 `time <= left` 的过期事件。
- `_pending_heap`：未来事件的最小时间堆，用于批量找到 `time <= watermark` 的激活事件。
- `_records`：保存尚未过期且未撤回的事件对象和活动/缓存状态，供按身份撤回。
- `_known_identities`：全局身份集合，精确拒绝重复摄入和撤回后的重放。

撤回不会立即从时间堆中删除条目，只删除 `_records` 中的身份记录并更新 Treap。对应堆项到达边界时被惰性识别并跳过。

## 复杂度

设：

- `n`：活动事件数。
- `d`：活动窗口中的不同值数量，`d <= n`。
- `p`：未来缓存事件数。
- `h`：活动时间堆当前长度，包含尚未到清理边界的惰性撤回项。
- `u`：未来时间堆当前长度，包含尚未到清理边界的惰性撤回项。
- `e`：生命周期内接收过的唯一身份总数。
- `k`：一次水位推进中跨左边界或右边界的堆项数，包含到达边界才清理的惰性撤回项。

| 操作 | 期望时间复杂度 |
| --- | --- |
| 活动值摄入 | `O(log d + log h)` |
| 未来事件摄入 | `O(log u)` |
| 重复摄入 | `O(1)` 平均 |
| 撤回活动事件 | `O(log d)` |
| 撤回未来事件 | `O(1)` 平均 |
| 精确分位数查询 | `O(log d)` |
| 一次水位推进 | `O(k(log h + log u + log d))`，无跨界堆项时为 `O(1)` |

Treap 使用随机优先级，树高是期望对数；分位数结果始终精确，不包含任何近似误差。每个堆项最多弹出一次，所以跨多次推进的总过期和激活成本期望为 `O(e log e)`。水位推进不会重新排序整个窗口，只让跨过边界的事件离开或进入 Treap。

空间占用：

- Treap：`O(d)`。
- 身份记录：`O(n + p)`。
- 两个时间堆：`O(h + u)`，其中包含尚未到达清理边界的惰性撤回项。
- 全局去重集合：`O(e)`；若需要无限历史去重，这是不可避免的主导空间。

因此总空间为 `O(e)`；不计算全局身份墓碑时为 `O(n + p + d + h + u)`，其中 `h`、`u` 的活动/缓存部分已与 `n`、`p` 重叠。

## 验证

```bash
python -m unittest discover -s tests -v
```

测试覆盖：

- `[1, 1, 9, 20]` 在 `q=0.5` 时为 `1`，撤回一条身份为独立事件的 `1` 后为 `9`。
- 左开右闭边界。
- 批量过期和大量相同值。
- 重复摄入、重复撤回、撤回后重放。
- `q=0`、`q=1` 和空窗。
- 40 组、每组 300 个随机摄入、撤回、推进和查询操作，与朴素排序 oracle 对照。
