# rle-history：带来源索引的状态历史运行长度编码

把设备状态历史压缩成「段」（run-length encoding），同时保留每条原始
事件的 `(source, seq)` 身份，支持增量编辑与补丁分发。Python 3.11+，零依赖。

## 数据模型

- **事件 `Event`**：`(time, state, source, seq)`。`time` 为整数；
  `(source, seq)` 是事件身份，重复插入会报错。
- **段 `Segment`**：连续相同状态合并成的**半开区间** `[start, end)`，
  `end is None` 表示延伸到 +∞。
  - `sources`：形成本段的全部事件身份——包括被合并的中间重复记录，
    合并不丢证据；
  - `overridden`：落在本段内、因同刻冲突被覆盖的事件身份。
- **补丁 `SegmentPatch`**：一次编辑的段级增量 `(removed, added)`。
  用 `apply_patch(segments, patch)` 施加到旧段列表即得新段列表；
  补丁与视图发散时抛 `ValueError`。

**同刻冲突规则**：同一时刻多条事件按 `(seq, source)` 字典序取最后一条
生效，其余进 `overridden`；撤回生效者后，被覆盖者自然重新生效。

## 接口

```python
from rle_history import StateHistory, apply_patch, build_segments

h = StateHistory()
h.insert(0, "A", "dev", 1)          # 返回 SegmentPatch
h.insert(2, "A", "dev", 2)
h.insert(5, "B", "dev", 3)
h.segments()        # [A[0,5) sources=(dev#1, dev#2), B[5,+inf) sources=(dev#3,)]
h.state_at(3)       # "A"
h.provenance(3)     # (dev#1, dev#2) —— 该段由哪些原始记录形成

patch = h.insert(1, "B", "dev", 4)  # 拆段：A[0,1) B[1,2) A[2,5)
patch = h.retract("dev", 4)         # 恢复原结构，A@2 证据仍在
patch = h.correct("dev", 3, state="C", time=None)  # 原子更正，单个补丁
```

编辑只重算受影响时间窗内的段，返回最小化补丁；窗口外的段对象不变。

## 补丁消费

下游维护物化视图，只消费补丁（完整示例见 `examples/patch_consumer.py`）：

```python
view = history.segments()           # 初始快照
view = apply_patch(view, patch)     # 之后每次编辑增量同步
```

## 正确性验证

`tests/test_state_history.py` 对每个场景做**三路径核对**：

1. 直接查询当前段 `h.segments()`；
2. 从初始段出发按序应用全部补丁（`apply_patch` 链）；
3. 用存活事件调参考实现 `build_segments(h.events())` 全量重算。

并检查结构不变量：段间首尾相接、相邻段状态不同、末段开放、
每条存活事件在某个段的 `sources`/`overridden` 中恰好出现一次。
另含 600 步随机操作（插入/撤回/更正）的属性测试。

```bash
python3 -m pytest tests/ -q
python3 examples/patch_consumer.py
```
